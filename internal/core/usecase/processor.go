package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	backgroundruntime "github.com/phlin/go-agent/internal/runtime/background"
	actionsvc "github.com/phlin/go-agent/internal/services/action"
	autonomysvc "github.com/phlin/go-agent/internal/services/autonomy"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
	multimodalsvc "github.com/phlin/go-agent/internal/services/multimodal"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
	personasvc "github.com/phlin/go-agent/internal/services/persona"
	profilesvc "github.com/phlin/go-agent/internal/services/profile"
)

type Planner interface {
	Plan(ctx context.Context, snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision) (replydomain.ReplyPlan, error)
}

type Processor struct {
	normalizer     *normalizersvc.Service
	context        *contextsvc.Service
	autonomy       *autonomysvc.Service
	planner        Planner
	executor       *actionsvc.Service
	memory         ports.MemoryStore
	state          ports.RuntimeStateStore
	groupWhitelist map[int64]struct{}
	personaID      string
	background     backgroundSubmitter

	// optional services (set via WithXxx options)
	memorySvc  *memsvc.Service
	profileSvc *profilesvc.Service
	memeSvc    *memesvc.Service
	visionSvc  *multimodalsvc.Service
	personaSvc *personasvc.Service
}

// ProcessorOption is a functional option for Processor.
type ProcessorOption func(*Processor)

type backgroundSubmitter interface {
	Submit(backgroundruntime.Job) bool
}

func WithBackgroundRuntime(runtime backgroundSubmitter) ProcessorOption {
	return func(p *Processor) { p.background = runtime }
}

func WithMemoryService(svc *memsvc.Service) ProcessorOption {
	return func(p *Processor) { p.memorySvc = svc }
}

func WithProfileService(svc *profilesvc.Service) ProcessorOption {
	return func(p *Processor) { p.profileSvc = svc }
}

func WithMemeService(svc *memesvc.Service) ProcessorOption {
	return func(p *Processor) { p.memeSvc = svc }
}

func WithVisionService(svc *multimodalsvc.Service) ProcessorOption {
	return func(p *Processor) { p.visionSvc = svc }
}

func WithPersonaService(svc *personasvc.Service) ProcessorOption {
	return func(p *Processor) { p.personaSvc = svc }
}

type ProcessResult struct {
	Envelope       conversationdomain.EventEnvelope   `json:"envelope"`
	Snapshot       conversationdomain.ContextSnapshot `json:"snapshot"`
	Decision       policydomain.AutonomyDecision      `json:"decision"`
	Plan           replydomain.ReplyPlan              `json:"plan"`
	Receipt        replydomain.ActionReceipt          `json:"receipt"`
	BackgroundJobs []BackgroundJobResult              `json:"background_jobs,omitempty"`
}

type BackgroundJobResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func NewProcessor(
	normalizer *normalizersvc.Service,
	ctxSvc *contextsvc.Service,
	autonomy *autonomysvc.Service,
	planner Planner,
	executor *actionsvc.Service,
	memory ports.MemoryStore,
	state ports.RuntimeStateStore,
	groupWhitelist []int64,
	personaID string,
	opts ...ProcessorOption,
) *Processor {
	wl := make(map[int64]struct{}, len(groupWhitelist))
	for _, gid := range groupWhitelist {
		wl[gid] = struct{}{}
	}
	p := &Processor{
		normalizer:     normalizer,
		context:        ctxSvc,
		autonomy:       autonomy,
		planner:        planner,
		executor:       executor,
		memory:         memory,
		state:          state,
		groupWhitelist: wl,
		personaID:      personaID,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Processor) ProcessRawEvent(ctx context.Context, payload []byte) (ProcessResult, error) {
	envelope, err := p.normalizer.Normalize(payload)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("normalize event: %w", err)
	}
	return p.ProcessEnvelope(ctx, envelope)
}

func (p *Processor) ProcessEnvelope(ctx context.Context, envelope conversationdomain.EventEnvelope) (ProcessResult, error) {
	textPreview := envelope.Event.Text
	if len(textPreview) > 60 {
		textPreview = textPreview[:60] + "..."
	}
	if envelope.Event.Kind == conversationdomain.EventMeta {
		slog.Debug("meta event ignored", "trace_id", envelope.TraceID)
		return ProcessResult{
			Envelope: envelope,
			Decision: policydomain.AutonomyDecision{
				DecisionID:  envelope.TraceID + "-ignored",
				Action:      policydomain.ActionSilent,
				StateBefore: policydomain.StateObserving,
				StateAfter:  policydomain.StateObserving,
				ReasonCodes: []string{"meta_event_ignored"},
				Explain:     map[string]float64{"meta_event_ignored": 1},
				Confidence:  1,
			},
		}, nil
	}

	if len(p.groupWhitelist) > 0 {
		if _, ok := p.groupWhitelist[envelope.Event.GroupID]; !ok {
			slog.Debug("group not whitelisted", "group_id", envelope.Event.GroupID, "trace_id", envelope.TraceID)
			return ProcessResult{
				Envelope: envelope,
				Decision: policydomain.AutonomyDecision{
					DecisionID:  fmt.Sprintf("decision-%d-filtered", time.Now().UnixNano()),
					Action:      policydomain.ActionSilent,
					StateBefore: policydomain.StateObserving,
					StateAfter:  policydomain.StateObserving,
					ReasonCodes: []string{"group_not_whitelisted"},
					Explain:     map[string]float64{"group_not_whitelisted": 1},
					Confidence:  1,
				},
			}, nil
		}
	}

	slog.Info("event received",
		"trace_id", envelope.TraceID,
		"kind", envelope.Event.Kind,
		"group_id", envelope.Event.GroupID,
		"user_id", envelope.Event.UserID,
		"attachments", len(envelope.Event.Attachments),
		"text", textPreview,
	)

	// Preserve request values for the synchronous compatibility path. The
	// Background Runtime owns cancellation for queued production jobs.
	bgCtx := context.WithoutCancel(ctx)
	backgroundJobs := make([]BackgroundJobResult, 0, 4)

	backgroundJobs = append(backgroundJobs, p.submitBackground(bgCtx, backgroundruntime.Job{
		Name:    "archive_event",
		Timeout: 3 * time.Second,
		Run: func(ctx context.Context) error {
			return p.memory.ArchiveEvent(ctx, envelope.Event)
		},
	}))

	// profileSvc 保持同步：ObserveEvent 写入的 profile 数据会被后续 BuildSnapshot 立即读取
	if p.profileSvc != nil {
		if err := p.profileSvc.ObserveEvent(ctx, envelope.Event); err != nil {
			return ProcessResult{}, fmt.Errorf("update member profile: %w", err)
		}
		if err := p.profileSvc.EnsureRelationshipInit(ctx, envelope.Event); err != nil {
			slog.Warn("ensure relationship init failed", "trace_id", envelope.TraceID, "err", err)
		}
	}

	// D-1: visionSvc.Understand 移到 memeSvc.ObserveEvent 之前，让后者能使用视觉描述符
	mediaDescriptors := []mediadomain.MediaDescriptor(nil)
	if p.visionSvc != nil && len(envelope.Event.Attachments) > 0 {
		descriptors, visionErr := p.visionSvc.Understand(ctx, envelope.Event.Attachments)
		if visionErr != nil {
			// P0-2: Vision 整体失败时记录带 trace_id 的 warn，方便定位
			slog.Warn("vision understand failed",
				"trace_id", envelope.TraceID,
				"attachments", len(envelope.Event.Attachments),
				"err", visionErr,
			)
		} else {
			mediaDescriptors = descriptors
		}
	}

	// B-3: memeSvc.ObserveEvent fire-and-forget，带超时；D-1 传入 mediaDescriptors
	if p.memeSvc != nil {
		svc := p.memeSvc
		ev := envelope.Event
		descs := mediaDescriptors
		backgroundJobs = append(backgroundJobs, p.submitBackground(bgCtx, backgroundruntime.Job{
			Name:    "meme_observe_event",
			Timeout: 5 * time.Second,
			Run: func(ctx context.Context) error {
				return svc.ObserveEvent(ctx, ev, descs)
			},
		}))
	}

	snapshot, err := p.context.BuildSnapshot(ctx, envelope, mediaDescriptors)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("build snapshot: %w", err)
	}

	decision, nextState, err := p.autonomy.Decide(ctx, snapshot)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("autonomy decide: %w", err)
	}
	slog.Info("autonomy decision",
		"trace_id", envelope.TraceID,
		"action", decision.Action,
		"trigger", decision.TriggerType,
		"reasons", decision.ReasonCodes,
		"confidence", decision.Confidence,
	)

	if err := p.state.SaveRuntimeState(ctx, nextState); err != nil {
		slog.Warn("save runtime state failed", "trace_id", envelope.TraceID, "err", err)
	}

	plan, err := p.planner.Plan(ctx, snapshot, decision)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("plan reply: %w", err)
	}
	slog.Info("reply plan",
		"trace_id", envelope.TraceID,
		"plan_id", plan.PlanID,
		"bubbles", len(plan.Bubbles),
		"actions", plan.PlannedActions,
	)

	receipt, err := p.executor.Execute(ctx, envelope.Event, decision, plan)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("execute action: %w", err)
	}

	// MarkIntent 是 reply 完成后的后置 trace 写入，改为 fire-and-forget
	if p.memorySvc != nil && decision.Action == policydomain.ActionReply && len(plan.Bubbles) > 0 {
		svc := p.memorySvc
		intent := memsvc.WriteIntent{
			Scope:         fmt.Sprintf("group:%d", envelope.Event.GroupID),
			MemoryType:    "reply_trace",
			Subject:       fmt.Sprintf("user:%d", envelope.Event.UserID),
			Content:       plan.Bubbles[0],
			SourceEventID: envelope.Event.EventID,
			Importance:    0.3,
			Confidence:    0.8,
		}
		backgroundJobs = append(backgroundJobs, p.submitBackground(bgCtx, backgroundruntime.Job{
			Name: "mark_reply_intent",
			Run: func(ctx context.Context) error {
				_, err := svc.MarkIntent(ctx, intent)
				return err
			},
		}))
	}

	// F2 Mood Loop: 在每次回复结束后异步更新情绪状态。
	// guard_silenced 算作"已回复"（内容被 OutputGuard 压制，但 bot 有意图），不计沉默。
	if p.personaSvc != nil {
		svc := p.personaSvc
		snap := snapshot
		dec := decision
		replied := receipt.Sent || receipt.DropReason == "guard_silenced"
		backgroundJobs = append(backgroundJobs, p.submitBackground(bgCtx, backgroundruntime.Job{
			Name:    "persona_mood_update",
			Timeout: 5 * time.Second,
			Run: func(ctx context.Context) error {
				return svc.UpdateAfterTurn(ctx, snap, dec, replied)
			},
		}))
	}

	return ProcessResult{
		Envelope:       envelope,
		Snapshot:       snapshot,
		Decision:       decision,
		Plan:           plan,
		Receipt:        receipt,
		BackgroundJobs: backgroundJobs,
	}, nil
}

func (p *Processor) submitBackground(ctx context.Context, job backgroundruntime.Job) BackgroundJobResult {
	if p.background != nil {
		if !p.background.Submit(job) {
			slog.Warn("background job dropped", "job", job.Name)
			return BackgroundJobResult{Name: job.Name, Status: "dropped"}
		}
		return BackgroundJobResult{Name: job.Name, Status: "queued"}
	}
	backgroundruntime.RunInline(ctx, job)
	return BackgroundJobResult{Name: job.Name, Status: "inline"}
}
