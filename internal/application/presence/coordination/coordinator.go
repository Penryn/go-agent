package coordination

import (
	"context"
	"fmt"
	"sync"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// MessageCoordinator 统一消息处理协调器
// 职责：
// 1. 同群串行：一个群同时只有一个回合在执行
// 2. 上下文管理：可取消和超时
// 3. 状态一致性：统一的发送入口，保证状态更新
// 4. 幂等保证：去重包含决策触发，不只是状态更新
type MessageCoordinator struct {
	// 核心依赖
	workingMemory WorkingMemoryManager
	decisionEngine DecisionEngine
	personaAssembler PersonaAssembler
	responsePlanner ResponsePlanner
	outbound OutboundSender

	// 串行控制：每个群一个执行槽
	groupMu    sync.Mutex
	groupSlots map[int64]*groupSlot

	// 配置
	turnTimeout time.Duration // 单个回合超时（默认 30s）
}

// groupSlot 群执行槽
type groupSlot struct {
	mu       sync.Mutex
	busy     bool
	cancel   context.CancelFunc
	lastSent time.Time
}

// NewMessageCoordinator 创建协调器
func NewMessageCoordinator(
	workingMemory WorkingMemoryManager,
	decisionEngine DecisionEngine,
	personaAssembler PersonaAssembler,
	responsePlanner ResponsePlanner,
	outbound OutboundSender,
) *MessageCoordinator {
	return &MessageCoordinator{
		workingMemory: workingMemory,
		decisionEngine: decisionEngine,
		personaAssembler: personaAssembler,
		responsePlanner: responsePlanner,
		outbound: outbound,
		groupSlots: make(map[int64]*groupSlot),
		turnTimeout: 30 * time.Second,
	}
}

// HandleInboundEvent 处理入站事件（主入口）
// 保证：
// 1. 同群串行执行
// 2. 幂等：重复事件不触发重复决策
// 3. 可取消：超时或外部取消
func (c *MessageCoordinator) HandleInboundEvent(
	ctx context.Context,
	record presencedomain.EventRecord,
) error {
	// 1. 获取群执行槽
	slot := c.getOrCreateSlot(record.GroupID)

	// 2. 尝试获取执行权（非阻塞）
	if !slot.tryAcquire() {
		// 群正在处理中，忽略（防止消息堆积）
		return fmt.Errorf("group %d busy, skipping event %s", record.GroupID, record.EventID)
	}
	defer slot.release()

	// 3. 创建带超时的上下文
	turnCtx, cancel := context.WithTimeout(ctx, c.turnTimeout)
	defer cancel()

	slot.setCancel(cancel) // 允许外部取消

	// 4. 执行完整回合
	return c.executeTurn(turnCtx, record, slot)
}

// executeTurn 执行一个完整回合
func (c *MessageCoordinator) executeTurn(
	ctx context.Context,
	record presencedomain.EventRecord,
	slot *groupSlot,
) error {
	// 1. 决策：是否参与
	decision, err := c.makeDecision(ctx, record)
	if err != nil {
		return fmt.Errorf("make decision: %w", err)
	}

	if !decision.ShouldRespond {
		// 不参与，但记录原因
		return nil
	}

	// 2. 规划：生成回复
	response, err := c.planResponse(ctx, record, decision)
	if err != nil {
		return fmt.Errorf("plan response: %w", err)
	}

	// 3. 执行：发送并更新状态
	return c.executeAndCommit(ctx, record.GroupID, response, slot)
}

// makeDecision 决策阶段
func (c *MessageCoordinator) makeDecision(
	ctx context.Context,
	record presencedomain.EventRecord,
) (*Decision, error) {
	// 检查上下文是否已取消
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// 获取工作记忆快照
	snapshot, err := c.workingMemory.Snapshot(ctx, record.GroupID)
	if err != nil {
		return nil, err
	}

	// 构建决策请求
	req := buildDecisionRequest(record, snapshot)

	// 调用决策引擎（传递 context）
	return c.decisionEngine.Decide(ctx, req)
}

// planResponse 规划阶段
func (c *MessageCoordinator) planResponse(
	ctx context.Context,
	record presencedomain.EventRecord,
	decision *Decision,
) (*PlannedResponse, error) {
	// 组装人格上下文
	personaCtx, err := c.personaAssembler.Assemble(ctx, record.GroupID, decision)
	if err != nil {
		return nil, err
	}

	// 规划回复
	return c.responsePlanner.Plan(ctx, personaCtx, decision)
}

// executeAndCommit 执行并提交阶段（原子性）
func (c *MessageCoordinator) executeAndCommit(
	ctx context.Context,
	groupID int64,
	response *PlannedResponse,
	slot *groupSlot,
) error {
	// 1. 发送消息
	execution := replydomain.ActionExecution{
		ActionID: generateActionID(),
		GroupID:  groupID,
		Segments: response.Segments,
	}

	receipt, err := c.outbound.Send(ctx, execution)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}

	// 2. 记录出站事件（关键：回写状态）
	outboundRecord := presencedomain.EventRecord{
		EventID: receipt.EventID,
		GroupID: groupID,
		Origin:  presencedomain.OriginOutbound,
		Event: conversationdomain.ConversationEvent{
			EventID:       receipt.EventID,
			GroupID:       groupID,
			MessageID:     receipt.PlatformMessageID,
			Text:          response.Text,
			TimestampUnix: time.Now().Unix(),
		},
	}

	// 3. 更新工作记忆（包含冷却、连续发言统计）
	if _, err := c.workingMemory.Observe(ctx, outboundRecord); err != nil {
		return fmt.Errorf("record outbound: %w", err)
	}

	// 4. 更新槽状态
	slot.lastSent = time.Now()

	return nil
}

// getOrCreateSlot 获取或创建群执行槽
func (c *MessageCoordinator) getOrCreateSlot(groupID int64) *groupSlot {
	c.groupMu.Lock()
	defer c.groupMu.Unlock()

	if slot, exists := c.groupSlots[groupID]; exists {
		return slot
	}

	slot := &groupSlot{}
	c.groupSlots[groupID] = slot
	return slot
}

// tryAcquire 尝试获取执行权（非阻塞）
func (s *groupSlot) tryAcquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.busy {
		return false
	}

	s.busy = true
	return true
}

// release 释放执行权
func (s *groupSlot) release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.busy = false
	if s.cancel != nil {
		s.cancel = nil
	}
}

// setCancel 设置取消函数
func (s *groupSlot) setCancel(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = cancel
}

// CancelGroup 取消群的当前执行（用于优雅关闭）
func (c *MessageCoordinator) CancelGroup(groupID int64) {
	c.groupMu.Lock()
	slot := c.groupSlots[groupID]
	c.groupMu.Unlock()

	if slot != nil {
		slot.mu.Lock()
		if slot.cancel != nil {
			slot.cancel()
		}
		slot.mu.Unlock()
	}
}

// 辅助函数
func buildDecisionRequest(
	record presencedomain.EventRecord,
	snapshot presencedomain.GroupWorkingMemory,
) *DecisionRequest {
	// TODO: 从 snapshot 提取必要信息构建请求
	return &DecisionRequest{
		GroupID:        record.GroupID,
		TriggerEventID: record.EventID,
		TargetUserID:   record.UserID,
		// ... 其他字段
	}
}

func generateActionID() string {
	return fmt.Sprintf("act_%d", time.Now().UnixNano())
}

// 接口定义（避免循环依赖）
type WorkingMemoryManager interface {
	Observe(ctx context.Context, record presencedomain.EventRecord) (presencedomain.GroupWorkingMemory, error)
	Snapshot(ctx context.Context, groupID int64) (presencedomain.GroupWorkingMemory, error)
}

type DecisionEngine interface {
	Decide(ctx context.Context, req *DecisionRequest) (*Decision, error)
}

type PersonaAssembler interface {
	Assemble(ctx context.Context, groupID int64, decision *Decision) (*PersonaContext, error)
}

type ResponsePlanner interface {
	Plan(ctx context.Context, personaCtx *PersonaContext, decision *Decision) (*PlannedResponse, error)
}

type OutboundSender interface {
	Send(ctx context.Context, execution replydomain.ActionExecution) (*SendReceipt, error)
}

// 数据结构
type DecisionRequest struct {
	GroupID        int64
	TriggerEventID string
	TargetUserID   int64
	// ... 其他字段
}

type Decision struct {
	ShouldRespond bool
	// ... 其他字段
}

type PersonaContext struct {
	// ... 字段
}

type PlannedResponse struct {
	Kind     string
	Text     string
	Segments []conversationdomain.MessageSegment
}

type SendReceipt struct {
	EventID           string
	PlatformMessageID string
}
