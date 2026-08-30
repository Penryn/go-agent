package turn_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/core/usecase"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	"github.com/phlin/go-agent/internal/runtime/turn"
)

type fakeProcessor struct {
	rawPayload []byte
	envelope   conversationdomain.EventEnvelope
	result     usecase.ProcessResult
	err        error
}

func (f *fakeProcessor) ProcessRawEvent(_ context.Context, payload []byte) (usecase.ProcessResult, error) {
	f.rawPayload = append([]byte(nil), payload...)
	return f.result, f.err
}

func (f *fakeProcessor) ProcessEnvelope(_ context.Context, envelope conversationdomain.EventEnvelope) (usecase.ProcessResult, error) {
	f.envelope = envelope
	return f.result, f.err
}

func TestRuntimeDelegatesEnvelopeAndProjectsOutcome(t *testing.T) {
	fake := &fakeProcessor{result: usecase.ProcessResult{
		Envelope: conversationdomain.EventEnvelope{TraceID: "trace-1"},
		Decision: policydomain.AutonomyDecision{Action: policydomain.ActionSilent},
	}}
	runtime := turn.New(fake)

	outcome, err := runtime.ProcessEnvelope(context.Background(), conversationdomain.EventEnvelope{TraceID: "input-trace"})
	if err != nil {
		t.Fatalf("process envelope: %v", err)
	}
	if fake.envelope.TraceID != "input-trace" {
		t.Fatalf("expected envelope to be delegated, got %q", fake.envelope.TraceID)
	}
	if outcome.Envelope.TraceID != "trace-1" || outcome.Decision.Action != policydomain.ActionSilent {
		t.Fatalf("unexpected outcome projection: %+v", outcome)
	}
	if len(outcome.Stages) != 1 || outcome.Stages[0].Name != "process" || outcome.Stages[0].Duration < 0 {
		t.Fatalf("unexpected stage trace: %+v", outcome.Stages)
	}
}

func TestRuntimeDelegatesRawEvent(t *testing.T) {
	fake := &fakeProcessor{}
	runtime := turn.New(fake)
	payload := []byte(`{"post_type":"message"}`)

	if _, err := runtime.ProcessRawEvent(context.Background(), payload); err != nil {
		t.Fatalf("process raw event: %v", err)
	}
	if string(fake.rawPayload) != string(payload) {
		t.Fatalf("unexpected raw payload: %q", fake.rawPayload)
	}
}

func TestRuntimeClassifiesFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind string
	}{
		{name: "invalid input", err: errors.New("normalize event: malformed payload"), kind: "invalid_input"},
		{name: "dependency", err: errors.New("send action: model unavailable"), kind: "dependency"},
		{name: "canceled", err: context.DeadlineExceeded, kind: "canceled"},
		{name: "internal", err: errors.New("unexpected failure"), kind: "internal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := turn.New(&fakeProcessor{err: tt.err})
			outcome, err := runtime.ProcessEnvelope(context.Background(), conversationdomain.EventEnvelope{TraceID: "trace"})
			if !errors.Is(err, tt.err) {
				t.Fatalf("expected original error, got %v", err)
			}
			if outcome.Failure == nil || outcome.Failure.Kind != tt.kind {
				t.Fatalf("unexpected failure classification: %+v", outcome.Failure)
			}
		})
	}
}

func TestRuntimeUsesInjectableClockForStageDuration(t *testing.T) {
	fake := &fakeProcessor{}
	first := time.Unix(100, 0)
	second := first.Add(25 * time.Millisecond)
	calls := 0
	clock := func() time.Time {
		calls++
		if calls == 1 {
			return first
		}
		return second
	}
	runtime := turn.New(fake, turn.WithClock(clock))
	outcome, err := runtime.ProcessEnvelope(context.Background(), conversationdomain.EventEnvelope{})
	if err != nil {
		t.Fatalf("process envelope: %v", err)
	}
	if outcome.Stages[0].Duration != 25*time.Millisecond {
		t.Fatalf("unexpected stage duration: %s", outcome.Stages[0].Duration)
	}
}

func TestRuntimeProjectsBackgroundJobResults(t *testing.T) {
	fake := &fakeProcessor{result: usecase.ProcessResult{
		BackgroundJobs: []usecase.BackgroundJobResult{{Name: "archive_event", Status: "queued"}},
	}}
	outcome, err := turn.New(fake).ProcessEnvelope(context.Background(), conversationdomain.EventEnvelope{})
	if err != nil {
		t.Fatalf("process envelope: %v", err)
	}
	if len(outcome.BackgroundJobs) != 1 || outcome.BackgroundJobs[0].Name != "archive_event" || outcome.BackgroundJobs[0].Status != "queued" {
		t.Fatalf("unexpected background jobs: %+v", outcome.BackgroundJobs)
	}
}
