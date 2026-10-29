package policy

import (
	"context"
	"time"
)

// Decision reason codes. They are stored on the attempt so admission can be
// aggregated without parsing log text.
const (
	DecisionSilent       = "silent"
	DecisionDirect       = "direct"
	DecisionContinuation = "continuation"
	DecisionSpontaneous  = "spontaneous"
	DecisionLimited      = "limited"
)

type Decision struct {
	ShouldGenerate bool
	TargetEventID  string
	TopicID        string
	ResponseMode   string
	Reason         string
}

type Input struct {
	GroupID             int64
	Directed            bool
	Continuing          bool
	HasNewInfo          bool
	RepliesLastMinute   int
	LastReplyAt         time.Time
	MaxPerMinute        int
	SpontaneousInterval time.Duration
}

type ResponsePolicy interface {
	Decide(context.Context, Input) Decision
}

// DeterministicPolicy enforces the hard timing and addressing boundaries
// before a request can consume model capacity. Model wording remains the
// generator's responsibility.
type DeterministicPolicy struct{}

func (DeterministicPolicy) Decide(_ context.Context, in Input) Decision {
	now := time.Now()
	if !in.Directed && !in.Continuing && !in.HasNewInfo {
		return Decision{ResponseMode: "silent", Reason: DecisionSilent}
	}
	if in.MaxPerMinute > 0 && in.RepliesLastMinute >= in.MaxPerMinute {
		return Decision{ResponseMode: "silent", Reason: DecisionLimited}
	}
	if !in.Directed && !in.Continuing && !in.LastReplyAt.IsZero() && in.SpontaneousInterval > 0 && now.Sub(in.LastReplyAt) < in.SpontaneousInterval {
		return Decision{ResponseMode: "silent", Reason: DecisionLimited}
	}
	mode := "short"
	reason := DecisionSpontaneous
	switch {
	case in.Directed:
		mode = "answer"
		reason = DecisionDirect
	case in.Continuing:
		mode = "short"
		reason = DecisionContinuation
	}
	return Decision{ShouldGenerate: true, ResponseMode: mode, Reason: reason}
}
