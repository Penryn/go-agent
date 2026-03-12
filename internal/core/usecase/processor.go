package usecase

import (
	"context"
	"fmt"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	actionsvc "github.com/phlin/go-agent/internal/services/action"
	autonomysvc "github.com/phlin/go-agent/internal/services/autonomy"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
	multimodalsvc "github.com/phlin/go-agent/internal/services/multimodal"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
	profilesvc "github.com/phlin/go-agent/internal/services/profile"
)

type Planner interface {
	Plan(ctx context.Context, snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision) (replydomain.ReplyPlan, error)
}

type Processor struct {
	normalizer *normalizersvc.Service
	context    *contextsvc.Service
	autonomy   *autonomysvc.Service
	planner    Planner
	executor   *actionsvc.Service
	memory     ports.MemoryStore
	state      ports.RuntimeStateStore
	memorySvc  *memsvc.Service
	profileSvc *profilesvc.Service
	memeSvc    *memesvc.Service
	visionSvc  *multimodalsvc.Service
}

type ProcessResult struct {
	Envelope conversationdomain.EventEnvelope   `json:"envelope"`
	Snapshot conversationdomain.ContextSnapshot `json:"snapshot"`
	Decision policydomain.AutonomyDecision      `json:"decision"`
	Plan     replydomain.ReplyPlan              `json:"plan"`
	Receipt  replydomain.ActionReceipt          `json:"receipt"`
}

func NewProcessor(normalizer *normalizersvc.Service, context *contextsvc.Service, autonomy *autonomysvc.Service, planner Planner, executor *actionsvc.Service, memory ports.MemoryStore, state ports.RuntimeStateStore, memorySvc *memsvc.Service, profileSvc *profilesvc.Service, memeSvc *memesvc.Service, visionSvc *multimodalsvc.Service) *Processor {
	return &Processor{
		normalizer: normalizer,
		context:    context,
		autonomy:   autonomy,
		planner:    planner,
		executor:   executor,
		memory:     memory,
		state:      state,
		memorySvc:  memorySvc,
		profileSvc: profileSvc,
		memeSvc:    memeSvc,
		visionSvc:  visionSvc,
	}
}

func (p *Processor) ProcessRawEvent(ctx context.Context, payload []byte) (ProcessResult, error) {
	envelope, err := p.normalizer.Normalize(payload)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("normalize event: %w", err)
	}
	return p.ProcessEnvelope(ctx, envelope)
}

func (p *Processor) ProcessEnvelope(ctx context.Context, envelope conversationdomain.EventEnvelope) (ProcessResult, error) {
	if envelope.Event.Kind == conversationdomain.EventMeta {
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

	if err := p.memory.ArchiveEvent(ctx, envelope.Event); err != nil {
		return ProcessResult{}, fmt.Errorf("archive event: %w", err)
	}
	if p.profileSvc != nil {
		if err := p.profileSvc.ObserveEvent(ctx, envelope.Event); err != nil {
			return ProcessResult{}, fmt.Errorf("update member profile: %w", err)
		}
	}
	if p.memeSvc != nil {
		if err := p.memeSvc.ObserveEvent(ctx, envelope.Event); err != nil {
			return ProcessResult{}, fmt.Errorf("collect meme candidate: %w", err)
		}
	}

	mediaDescriptors := []mediadomain.MediaDescriptor(nil)
	if p.visionSvc != nil && len(envelope.Event.Attachments) > 0 {
		if descriptors, err := p.visionSvc.Understand(ctx, envelope.Event.Attachments); err == nil {
			mediaDescriptors = descriptors
		}
	}

	snapshot, err := p.context.BuildSnapshot(ctx, envelope, mediaDescriptors)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("build snapshot: %w", err)
	}

	decision, nextState, err := p.autonomy.Decide(ctx, snapshot)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("autonomy decide: %w", err)
	}

	if err := p.state.SaveRuntimeState(ctx, nextState); err != nil {
		return ProcessResult{}, fmt.Errorf("save runtime state: %w", err)
	}

	plan, err := p.planner.Plan(ctx, snapshot, decision)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("plan reply: %w", err)
	}

	receipt, err := p.executor.Execute(ctx, envelope.Event, decision, plan)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("execute action: %w", err)
	}
	if p.memorySvc != nil && decision.Action == policydomain.ActionReply && len(plan.Bubbles) > 0 {
		if _, err := p.memorySvc.MarkIntent(ctx, memsvc.WriteIntent{
			Scope:         fmt.Sprintf("group:%d", envelope.Event.GroupID),
			MemoryType:    "reply_trace",
			Subject:       fmt.Sprintf("user:%d", envelope.Event.UserID),
			Content:       plan.Bubbles[0],
			SourceEventID: envelope.Event.EventID,
			Importance:    0.3,
			Confidence:    0.8,
		}); err != nil {
			return ProcessResult{}, fmt.Errorf("write reply trace memory: %w", err)
		}
	}

	return ProcessResult{
		Envelope: envelope,
		Snapshot: snapshot,
		Decision: decision,
		Plan:     plan,
		Receipt:  receipt,
	}, nil
}
