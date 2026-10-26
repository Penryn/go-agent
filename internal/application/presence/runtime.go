// Package presence orchestrates the AI group friend event and turn lifecycle.
package presence

import (
	"context"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/application/action"
	"github.com/phlin/go-agent/internal/application/modelusage"
	normalizersvc "github.com/phlin/go-agent/internal/application/normalizer"
	personasvc "github.com/phlin/go-agent/internal/application/persona"
	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/application/presence/deliberation"
	groupactor "github.com/phlin/go-agent/internal/application/presence/group_actor"
	workspaceapp "github.com/phlin/go-agent/internal/application/workspace"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type Config struct {
	GroupWhitelist []int64
}

type PerceptionSubmitter interface {
	Submit(presencedomain.EventRecord)
}

type ConfirmationObserver interface {
	ObserveConfirmation(groupID, userID int64, text string, at time.Time)
}

type FeedbackObserver interface {
	ObserveInbound(context.Context, conversationdomain.ConversationEvent) error
	ObserveSent(context.Context, conversationdomain.ConversationEvent, replydomain.ActionReceipt, string) error
}

// EventObserverFunc 在每条入站事件后收到回调；错误只记日志。
type EventObserverFunc func(context.Context, conversationdomain.ConversationEvent) error

// TurnObserver closes the feedback loop between a realized action and future
// scheduling. Implementations own durable cooldown, persona, and reflection
// state; Runtime only invokes the narrow lifecycle hooks.
type TurnObserver interface {
	ObserveInbound(context.Context, conversationdomain.ConversationEvent) error
	CanDeliberate(context.Context, int64, time.Time) (bool, error)
	AfterTurn(context.Context, conversationdomain.ContextSnapshot, policydomain.AutonomyDecision, replydomain.ActionReceipt) error
}

// pollInterval keeps actor reclamation responsive without a busy loop.
const pollInterval = 100 * time.Millisecond

type Outcome struct {
	Envelope conversationdomain.EventEnvelope   `json:"envelope"`
	Snapshot conversationdomain.ContextSnapshot `json:"snapshot"`
	Decision policydomain.AutonomyDecision      `json:"decision"`
	Plan     replydomain.ReplyPlan              `json:"plan"`
	Receipt  replydomain.ActionReceipt          `json:"receipt"`
}

type Runtime struct {
	deps      Dependencies
	whitelist map[int64]struct{}

	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	turnMu      sync.Mutex
	activeTurns map[int64]inFlightTurn
	turnSeq     uint64
}

type inFlightTurn struct {
	sequence uint64
	cancel   context.CancelFunc
}

const deliberationTimeout = 45 * time.Second
const postSendHookTimeout = 5 * time.Second

// Dependencies is assembled once before the runtime starts. Event projections,
// turn feedback and optional tools all participate in the same lifecycle.
type Dependencies struct {
	Normalizer     *normalizersvc.Service
	WorkingMemory  *groupactor.Manager
	Deliberator    deliberation.Deliberator
	Perception     PerceptionSubmitter
	Confirmations  ConfirmationObserver
	Feedback       FeedbackObserver
	Thoughts       ports.ThoughtStore
	ModelUsage     modelusage.Sink
	Turns          TurnObserver
	Canon          *personasvc.CanonService
	Executor       *action.Service
	Workspaces     *workspaceapp.Manager
	EventObservers []EventObserverFunc
}

func New(parent context.Context, deps Dependencies, cfg Config) *Runtime {
	deps.EventObservers = append([]EventObserverFunc(nil), deps.EventObservers...)
	ctx, cancel := context.WithCancel(parent)
	r := &Runtime{
		deps:        deps,
		whitelist:   make(map[int64]struct{}, len(cfg.GroupWhitelist)),
		ctx:         ctx,
		cancel:      cancel,
		activeTurns: make(map[int64]inFlightTurn),
	}
	for _, groupID := range cfg.GroupWhitelist {
		r.whitelist[groupID] = struct{}{}
	}
	r.wg.Add(1)
	go r.loop(pollInterval)
	return r
}
