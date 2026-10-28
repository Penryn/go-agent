package policy

import "context"

type Decision struct {
	ShouldGenerate bool
	TargetEventID  string
	TopicID        string
	ResponseMode   string
	Reason         string
}

type Input struct {
	GroupID      int64
	Directed     bool
	Continuing   bool
	HasNewInfo   bool
	WithinBudget bool
}

type ResponsePolicy interface {
	Decide(context.Context, Input) Decision
}

// DeterministicPolicy contains only admission rules. Model wording remains
// the generator's responsibility, while this policy enforces the hard timing
// and addressing boundaries before a request can consume model capacity.
type DeterministicPolicy struct{}

func (DeterministicPolicy) Decide(_ context.Context, in Input) Decision {
	if !in.WithinBudget {
		return Decision{ResponseMode: "silent", Reason: "budget"}
	}
	if !in.Directed && !in.Continuing && !in.HasNewInfo {
		return Decision{ResponseMode: "silent", Reason: "ordinary_no_new_info"}
	}
	mode := "short"
	if in.Directed {
		mode = "answer"
	}
	return Decision{ShouldGenerate: true, ResponseMode: mode, Reason: "eligible"}
}
