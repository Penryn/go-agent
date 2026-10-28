package policy

import (
	"context"
	"testing"
)

func TestDeterministicPolicyHonorsBudgetAndAddressing(t *testing.T) {
	p := DeterministicPolicy{}
	if d := p.Decide(context.Background(), Input{WithinBudget: false, Directed: true}); d.ShouldGenerate || d.Reason != "budget" {
		t.Fatalf("unexpected budget decision: %+v", d)
	}
	if d := p.Decide(context.Background(), Input{WithinBudget: true}); d.ShouldGenerate {
		t.Fatalf("ordinary empty input generated: %+v", d)
	}
	if d := p.Decide(context.Background(), Input{WithinBudget: true, Directed: true}); !d.ShouldGenerate || d.ResponseMode != "answer" {
		t.Fatalf("direct input not admitted: %+v", d)
	}
}
