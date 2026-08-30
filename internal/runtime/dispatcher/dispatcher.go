// Package dispatcher 实现 per-group 串行消息队列。
//
// 设计目标：
//   - 同一群内消息严格串行（消除 RuntimeState 读-改-写竞态）
//   - 不同群之间完全并行（group worker 相互独立）
//   - direct_triggered（@bot）消息绝对不 drop，走独立优先通道
//   - 普通消息在队列满时 drop 并记录 warn 日志
//   - 每条消息有独立处理超时，防止单条消息阻塞整个群队列
//   - idle worker 超时自动退出，不无限持有资源
//
// # 方案选择：direct_triggered 保障
//
// 选择【方案A】：dispatcher 在入队前先做一次轻量 Normalize，
// 仅用于判断是否 direct_triggered（@bot / 名字 / 回复bot），
// 随后将已解析的 envelope 入队。worker 侧调用
// EnvelopeProcessor.ProcessEnvelope（接受已解析 envelope），
// 从而 Normalize 只在 dispatcher 侧执行一次，无 IO 代价极低。
//
// 相比方案B（priority-channel + heap/deque，实现复杂）、
// 方案C（两套 channel 需要轻量预解析），方案A 代码最简单、
// 优先级语义最清晰，且不额外引入任何依赖。
//
// # 消息流
//
//	Dispatch(payload)
//	    │
//	    ├─ normalizer.Normalize(payload)   // 纯 JSON 解析，判断 direct_triggered
//	    │
//	    ├─ GroupID == 0? ──YES──► 直接异步处理（私聊/meta，不走群队列）
//	    │
//	    ├─ direct_triggered? ──YES──► priorityBuf（无界 slice）──► drainLoop ──► priorityCh
//	    │                   └──NO──► normalCh（有界，满则 drop + warn）
//	    │
//	    └─ 若 worker 未启动 → 惰性启动 worker goroutine
//
//	worker goroutine（每群一个，串行消费）：
//	    loop {
//	        先 non-blocking 检查 priorityCh（双层 select 保证 priority 优先）
//	        再 blocking select { priorityCh | normalCh | idleTimer | ctx.Done }
//	        带超时 context 调用 EnvelopeProcessor.ProcessEnvelope
//	    }
package dispatcher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
)

// ─────────────────────────────────────────────────────────────────────────────
// 公开配置
// ─────────────────────────────────────────────────────────────────────────────

// Config 是 GroupDispatcher 的可调参数，所有字段均有合理默认值（通过 DefaultConfig）。
type Config struct {
	// NormalQueueSize 是每个群普通消息队列的容量；超出后新消息被 drop。
	NormalQueueSize int

	// JobTimeout 是每条消息允许的最长处理时间；超时后 context 被取消。
	JobTimeout time.Duration

	// IdleTimeout 是 worker 无消息可处理后的退出等待时间。
	IdleTimeout time.Duration
}

// DefaultConfig 返回推荐的生产默认值。
func DefaultConfig() Config {
	return Config{
		NormalQueueSize: 64,
		JobTimeout:      120 * time.Second,
		IdleTimeout:     5 * time.Minute,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// EnvelopeProcessor 接口
// ─────────────────────────────────────────────────────────────────────────────

// EnvelopeProcessor 是 GroupDispatcher 对业务处理器的最小依赖。
// 仅声明返回 error，调用方（dispatcher）不需要处理业务返回值。
//
// 适配 *usecase.Processor 时，在 app.go 用以下闭包封装：
//
//	dispatcher.ProcessorFunc(func(ctx context.Context, env conversationdomain.EventEnvelope) error {
//	    _, err := processor.ProcessEnvelope(ctx, env)
//	    return err
//	})
type EnvelopeProcessor interface {
	ProcessEnvelope(ctx context.Context, envelope conversationdomain.EventEnvelope) error
}

// ProcessorFunc 是函数类型的 EnvelopeProcessor 适配器，方便匿名实现接口。
type ProcessorFunc func(ctx context.Context, envelope conversationdomain.EventEnvelope) error

// ProcessEnvelope 实现 EnvelopeProcessor 接口。
func (f ProcessorFunc) ProcessEnvelope(ctx context.Context, envelope conversationdomain.EventEnvelope) error {
	return f(ctx, envelope)
}

// ─────────────────────────────────────────────────────────────────────────────
// 内部类型
// ─────────────────────────────────────────────────────────────────────────────

// job 是进入群队列的工作单元，携带已解析的 envelope。
type job struct {
	envelope conversationdomain.EventEnvelope
	traceID  string // 冗余存放，方便日志输出
}

// groupWorker 代表单个群的串行处理单元。
type groupWorker struct {
	groupID  int64
	normalCh chan job // 有界普通消息通道，满则 drop

	// priorityCh 是送入 worker 主循环的优先消息通道（容量 1）。
	// drainLoop 负责将 priorityBuf 中的消息逐个喂入，
	// 对 Dispatch 侧永不阻塞。
	priorityCh    chan job
	priorityBuf   []job          // 无界软缓冲，保存待喂入的 priority job
	priorityMu    sync.Mutex     // 保护 priorityBuf
	priorityNotif chan struct{}   // 有新 priority job 时通知 drainLoop

	stopDrain chan struct{}    // 关闭 drainLoop goroutine 的信号
	cancel    context.CancelFunc // 终止 worker（含 drainLoop）的 context cancel
}

// ─────────────────────────────────────────────────────────────────────────────
// GroupDispatcher
// ─────────────────────────────────────────────────────────────────────────────

// GroupDispatcher 管理所有群的 worker，线程安全。
// 通过 [New] 创建，通过 [GroupDispatcher.Dispatch] 分发消息。
type GroupDispatcher struct {
	cfg        Config
	normalizer *normalizersvc.Service
	processor  EnvelopeProcessor

	mu      sync.Mutex
	workers map[int64]*groupWorker

	// appCtx 绑定应用生命周期；canceled 时所有 worker 自然退出。
	appCtx context.Context
}

// New 创建 GroupDispatcher。
//
//   - appCtx：应用生命周期 context，canceled 时所有 worker 自然退出
//   - normalizer：用于 Dispatch 时的轻量解析（判断 direct_triggered）
//   - processor：实现 EnvelopeProcessor 的业务处理器
//   - cfg：调度参数，建议用 [DefaultConfig] 作为起点
func New(
	appCtx context.Context,
	normalizer *normalizersvc.Service,
	processor EnvelopeProcessor,
	cfg Config,
) *GroupDispatcher {
	return &GroupDispatcher{
		cfg:        cfg,
		normalizer: normalizer,
		processor:  processor,
		workers:    make(map[int64]*groupWorker),
		appCtx:     appCtx,
	}
}

// Dispatch 是消息入口，可从任意 goroutine 并发调用，不阻塞调用方。
//
// 处理步骤：
//  1. Normalize payload（纯 JSON 解析，无 IO）
//  2. GroupID == 0（私聊/meta）→ 单独 goroutine 直接处理，跳过群队列
//  3. 根据 direct_triggered 标志路由到 priority 或 normal 通道
//  4. 若目标群 worker 尚未启动，惰性创建
func (d *GroupDispatcher) Dispatch(ctx context.Context, payload []byte) {
	// ── Step 1: 轻量 Normalize，读取 GroupID 与 direct_triggered 标志 ──
	envelope, err := d.normalizer.Normalize(payload)
	if err != nil {
		slog.Warn("dispatcher: normalize failed, payload dropped",
			"error", err,
			"payload_len", len(payload),
		)
		return
	}

	groupID := envelope.Event.GroupID
	traceID := envelope.TraceID

	// ── Step 2: 非群消息直接异步处理，不占用群队列 ──
	if groupID == 0 {
		go func() {
			jobCtx, cancel := context.WithTimeout(d.appCtx, d.cfg.JobTimeout)
			defer cancel()
			if err := d.processor.ProcessEnvelope(jobCtx, envelope); err != nil {
				slog.Error("dispatcher: process non-group event failed",
					"trace_id", traceID,
					"error", err,
				)
			}
		}()
		return
	}

	// ── Step 3: 判断是否 direct_triggered ──
	isDirect := envelope.Event.MentionedBot ||
		envelope.Event.NamedBot ||
		envelope.Event.IsReplyToBot

	j := job{envelope: envelope, traceID: traceID}

	// ── Step 4: 获取或惰性创建 worker ──
	d.mu.Lock()
	w, ok := d.workers[groupID]
	if !ok {
		w = d.newWorker(groupID)
		d.workers[groupID] = w
	}
	d.mu.Unlock()

	// ── Step 5: 入队 ──
	if isDirect {
		// priority：追加到无界缓冲，永不 drop
		w.pushPriority(j)
		slog.Debug("dispatcher: priority job enqueued",
			"group_id", groupID,
			"trace_id", traceID,
		)
	} else {
		// normal：有界通道，满则 drop 并记 warn
		select {
		case w.normalCh <- j:
		default:
			slog.Warn("dispatcher: normal queue full, message dropped",
				"group_id", groupID,
				"trace_id", traceID,
			)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// worker 生命周期
// ─────────────────────────────────────────────────────────────────────────────

// newWorker 创建并启动一个群 worker（含 drainLoop）。
// 调用方必须持有 d.mu。
func (d *GroupDispatcher) newWorker(groupID int64) *groupWorker {
	// workerCtx 绑定 appCtx，程序退出时 worker 自动感知
	workerCtx, cancel := context.WithCancel(d.appCtx)

	w := &groupWorker{
		groupID:       groupID,
		priorityCh:    make(chan job, 1), // 容量 1；drainLoop 负责从 priorityBuf 逐个喂入
		normalCh:      make(chan job, d.cfg.NormalQueueSize),
		priorityNotif: make(chan struct{}, 1),
		stopDrain:     make(chan struct{}),
		cancel:        cancel,
	}

	// drainLoop：将 priorityBuf 中的 job 逐个推入 priorityCh
	go w.drainLoop(workerCtx)

	// 主 worker goroutine：串行消费两个通道
	go d.runWorker(workerCtx, w)

	slog.Info("dispatcher: worker started", "group_id", groupID)
	return w
}

// removeWorker 在 worker 退出后将自身从 map 摘除，允许下次消息到达时惰性重建。
func (d *GroupDispatcher) removeWorker(groupID int64) {
	d.mu.Lock()
	delete(d.workers, groupID)
	d.mu.Unlock()
}

// runWorker 是每群一个的串行处理 goroutine。
//
// 优先级调度策略：
//   - 先执行一次 non-blocking select 尝试消费 priorityCh
//   - 若 priorityCh 为空，进入 blocking select（priority / normal / idle / ctx）
//
// 这样在 normal 消息堆积时，priority 消息仍能被优先消费。
func (d *GroupDispatcher) runWorker(ctx context.Context, w *groupWorker) {
	defer func() {
		// worker 退出：关闭 drainLoop，从 map 摘除自身
		close(w.stopDrain)
		w.cancel()
		d.removeWorker(w.groupID)
		slog.Info("dispatcher: worker stopped", "group_id", w.groupID)
	}()

	idleTimer := time.NewTimer(d.cfg.IdleTimeout)
	defer idleTimer.Stop()

	for {
		// ── 双层 select：优先消费 priorityCh ──
		select {
		case j := <-w.priorityCh:
			// 有 priority 消息立即处理，重置 idle 计时器
			resetTimer(idleTimer, d.cfg.IdleTimeout)
			d.processJob(ctx, j)
			continue
		default:
			// priorityCh 当前为空，进入公平 select
		}

		select {
		case <-ctx.Done():
			return

		case j := <-w.priorityCh:
			resetTimer(idleTimer, d.cfg.IdleTimeout)
			d.processJob(ctx, j)

		case j := <-w.normalCh:
			resetTimer(idleTimer, d.cfg.IdleTimeout)
			d.processJob(ctx, j)

		case <-idleTimer.C:
			// idle 超时，worker 正常退出；下次有消息时会被惰性重建
			slog.Info("dispatcher: worker idle timeout, exiting",
				"group_id", w.groupID,
				"idle_timeout", d.cfg.IdleTimeout,
			)
			return
		}
	}
}

// processJob 执行单条消息，附带独立的超时 context。
// 超时后 context 被取消，下游 IO 操作会自动中断。
func (d *GroupDispatcher) processJob(ctx context.Context, j job) {
	jobCtx, cancel := context.WithTimeout(ctx, d.cfg.JobTimeout)
	defer cancel()

	if err := d.processor.ProcessEnvelope(jobCtx, j.envelope); err != nil {
		slog.Error("dispatcher: process envelope failed",
			"group_id", j.envelope.Event.GroupID,
			"trace_id", j.traceID,
			"error", err,
		)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// priorityCh 的无界软缓冲（drainLoop + pushPriority）
// ─────────────────────────────────────────────────────────────────────────────

// pushPriority 将 job 追加到 priorityBuf（无界），并非阻塞地通知 drainLoop。
// 对 Dispatch 调用方永不阻塞、永不 drop。
func (w *groupWorker) pushPriority(j job) {
	w.priorityMu.Lock()
	w.priorityBuf = append(w.priorityBuf, j)
	w.priorityMu.Unlock()

	// 非阻塞通知；若 drainLoop 已有待处理通知则无需重复发送
	select {
	case w.priorityNotif <- struct{}{}:
	default:
	}
}

// drainLoop 将 priorityBuf 中的 job 逐个转发到 priorityCh（容量 1）。
// worker 主循环从 priorityCh 读取，代码路径清晰无锁。
func (w *groupWorker) drainLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopDrain:
			return
		case <-w.priorityNotif:
			// 将 buf 中当前所有 job 依次推入 priorityCh
			for {
				w.priorityMu.Lock()
				if len(w.priorityBuf) == 0 {
					w.priorityMu.Unlock()
					break
				}
				j := w.priorityBuf[0]
				// 移除头部元素，保持 FIFO 顺序
				w.priorityBuf = w.priorityBuf[1:]
				w.priorityMu.Unlock()

				// 阻塞等待 worker 从 priorityCh 取走上一个 job 后再推入下一个
				select {
				case w.priorityCh <- j:
				case <-ctx.Done():
					return
				case <-w.stopDrain:
					return
				}
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────────────────

// resetTimer 安全地重置一个已启动的 Timer，遵循 Go 官方推荐的序列：
// Stop → 排空已触发事件 → Reset。
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
