// Package turn owns the public boundary for one real-time conversation turn.
// The initial implementation delegates to the existing processor so callers
// can migrate to this boundary without changing turn behavior.
package turn

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/core/usecase"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// Processor is the compatibility seam used by Runtime while the old
// processor implementation is being migrated into this module.
type Processor interface {
	ProcessRawEvent(context.Context, []byte) (usecase.ProcessResult, error)
	ProcessEnvelope(context.Context, conversationdomain.EventEnvelope) (usecase.ProcessResult, error)
}

// StageTrace records the duration of a coarse turn stage. More detailed stages
// can be added as implementation moves behind Runtime.
type StageTrace struct {
	Name     string        `json:"name" yaml:"name"`
	Duration time.Duration `json:"duration" yaml:"duration"`
}

// Failure describes a turn error without requiring callers to parse an error
// string. Kind is intentionally small and stable across implementation moves.
type Failure struct {
	Stage string `json:"stage" yaml:"stage"`
	Kind  string `json:"kind" yaml:"kind"`
}

// Outcome is the auditable result of a turn. The existing processor fields are
// retained during migration; callers should depend on this type instead of
// usecase.ProcessResult.
type Outcome struct {
	Envelope conversationdomain.EventEnvelope   `json:"envelope"`
	Snapshot conversationdomain.ContextSnapshot `json:"snapshot"`
	Decision policydomain.AutonomyDecision      `json:"decision"`
	Plan     replydomain.ReplyPlan              `json:"plan"`
	Receipt  replydomain.ActionReceipt          `json:"receipt"`
	Stages   []StageTrace                       `json:"stages,omitempty"`
	Failure  *Failure                           `json:"failure,omitempty"`
}

// Runtime is the real-time turn boundary.
type Runtime struct {
	processor Processor
	clock     func() time.Time
}

// Option customizes Runtime internals that need deterministic tests.
type Option func(*Runtime)

// WithClock replaces the clock used for stage timing.
func WithClock(clock func() time.Time) Option {
	return func(r *Runtime) {
		if clock != nil {
			r.clock = clock
		}
	}
}

// New creates a Runtime around the current processor implementation.
func New(processor Processor, opts ...Option) *Runtime {
	runtime := &Runtime{processor: processor, clock: time.Now}
	for _, opt := range opts {
		opt(runtime)
	}
	return runtime
}

// ProcessRawEvent normalizes and executes one raw inbound event through the
// compatibility processor.
func (r *Runtime) ProcessRawEvent(ctx context.Context, payload []byte) (Outcome, error) {
	started := r.clock()
	if r.processor == nil {
		err := errors.New("turn runtime: processor is nil")
		return Outcome{Failure: &Failure{Stage: "process", Kind: classifyError(err)}}, err
	}
	result, err := r.processor.ProcessRawEvent(ctx, payload)
	outcome := fromProcessResult(result)
	outcome.Stages = []StageTrace{{Name: "process", Duration: r.clock().Sub(started)}}
	if err != nil {
		outcome.Failure = &Failure{Stage: "process", Kind: classifyError(err)}
	}
	return outcome, err
}

// ProcessEnvelope executes an already normalized event through the
// compatibility processor.
func (r *Runtime) ProcessEnvelope(ctx context.Context, envelope conversationdomain.EventEnvelope) (Outcome, error) {
	started := r.clock()
	if r.processor == nil {
		err := errors.New("turn runtime: processor is nil")
		return Outcome{Envelope: envelope, Failure: &Failure{Stage: "process", Kind: classifyError(err)}}, err
	}
	result, err := r.processor.ProcessEnvelope(ctx, envelope)
	outcome := fromProcessResult(result)
	outcome.Stages = []StageTrace{{Name: "process", Duration: r.clock().Sub(started)}}
	if err != nil {
		outcome.Failure = &Failure{Stage: "process", Kind: classifyError(err)}
	}
	return outcome, err
}

func fromProcessResult(result usecase.ProcessResult) Outcome {
	return Outcome{
		Envelope: result.Envelope,
		Snapshot: result.Snapshot,
		Decision: result.Decision,
		Plan:     result.Plan,
		Receipt:  result.Receipt,
	}
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"normalize", "invalid", "malformed"} {
		if strings.Contains(message, marker) {
			return "invalid_input"
		}
	}
	for _, marker := range []string{"model", "memory", "store", "send", "outbound", "vision"} {
		if strings.Contains(message, marker) {
			return "dependency"
		}
	}
	return "internal"
}
