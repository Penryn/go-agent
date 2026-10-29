package policy

import (
	"context"
	"testing"
	"time"
)

func TestDeterministicPolicyHonorsBudgetAndAddressing(t *testing.T) {
	p := DeterministicPolicy{}
	now := time.Now()
	if d := p.Decide(context.Background(), Input{RepliesLastMinute: 3, MaxPerMinute: 3, Directed: true}); d.ShouldGenerate || d.Reason != DecisionLimited {
		t.Fatalf("unexpected budget decision: %+v", d)
	}
	if d := p.Decide(context.Background(), Input{MaxPerMinute: 3}); d.ShouldGenerate || d.Reason != DecisionSilent {
		t.Fatalf("ordinary empty input generated: %+v", d)
	}
	if d := p.Decide(context.Background(), Input{HasNewInfo: true, MaxPerMinute: 3, LastReplyAt: now, SpontaneousInterval: time.Minute}); d.ShouldGenerate || d.Reason != DecisionLimited {
		t.Fatalf("spontaneous interval ignored: %+v", d)
	}
	if d := p.Decide(context.Background(), Input{Directed: true, MaxPerMinute: 3, LastReplyAt: now, SpontaneousInterval: time.Minute}); !d.ShouldGenerate || d.ResponseMode != "answer" || d.Reason != DecisionDirect {
		t.Fatalf("direct input not admitted: %+v", d)
	}
	if d := p.Decide(context.Background(), Input{Continuing: true, MaxPerMinute: 3, LastReplyAt: now, SpontaneousInterval: time.Minute}); !d.ShouldGenerate || d.Reason != DecisionContinuation {
		t.Fatalf("continuation not admitted: %+v", d)
	}
}
