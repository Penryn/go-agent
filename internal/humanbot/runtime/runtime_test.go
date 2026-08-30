package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	"github.com/phlin/go-agent/internal/humanbot/runtime/deliberation"
	"github.com/phlin/go-agent/internal/humanbot/runtime/group_actor"
	"github.com/phlin/go-agent/internal/humanbot/runtime/ingress"
	"github.com/phlin/go-agent/internal/services/action"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
	"github.com/phlin/go-agent/internal/services/normalizer"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
	promptingsvc "github.com/phlin/go-agent/internal/services/prompting"
)

type failingDeliberator struct{}

func (failingDeliberator) Deliberate(context.Context, deliberation.Input) (deliberation.Result, error) {
	return deliberation.Result{}, errors.New("deliberation failed")
}

func TestProcessRawEventUsesCandidateRuntime(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.QQ.SelfID = 123456
	cfg.DefaultPolicy.QuietHours = nil
	store := inmemory.NewStore()
	policy := policysvc.New(cfg)
	normalizer := normalizer.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)
	eventLog := ingress.NewMemoryEventLog()
	working := group_actor.NewManager(eventLog)
	defer working.Close()
	contextService := contextsvc.New(store, ports.NoopVectorStore{}, store, store, policy, cfg.Persona)
	contextService.WithWorkingMemory(working)
	planner := promptingsvc.NewDeterministicPlanner(cfg.Persona)
	sender := inmemory.NewSender()
	executor := action.New(sender, nil, nil, action.WithPresenceObserver(working), action.WithSelfID(cfg.QQ.SelfID))
	runtime := New(ctx, normalizer, working, deliberation.NewAdapter(contextService, planner), nil, nil, executor, Config{SelfID: cfg.QQ.SelfID})
	defer runtime.Close()

	payload := []byte(`{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"m1","message":[{"type":"text","data":{"text":"hello?"}}]}`)
	outcome, err := runtime.ProcessRawEvent(ctx, payload)
	if err != nil {
		t.Fatalf("process raw event: %v", err)
	}
	if outcome.Decision.Action != policydomain.ActionReply || !outcome.Receipt.Sent {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if len(outcome.Snapshot.RecentTurns) == 0 || outcome.Snapshot.RecentTurns[len(outcome.Snapshot.RecentTurns)-1].Kind != conversationdomain.EventMessage {
		t.Fatalf("current event missing from context: %+v", outcome.Snapshot.RecentTurns)
	}
}

func TestProcessRawEventRespectsMinimumCandidateScore(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.QQ.SelfID = 123456
	cfg.DefaultPolicy.QuietHours = nil
	store := inmemory.NewStore()
	policy := policysvc.New(cfg)
	normalizer := normalizer.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)
	eventLog := ingress.NewMemoryEventLog()
	working := group_actor.NewManager(eventLog)
	defer working.Close()
	contextService := contextsvc.New(store, ports.NoopVectorStore{}, store, store, policy, cfg.Persona)
	contextService.WithWorkingMemory(working)
	planner := promptingsvc.NewDeterministicPlanner(cfg.Persona)
	sender := inmemory.NewSender()
	executor := action.New(sender, nil, nil, action.WithPresenceObserver(working), action.WithSelfID(cfg.QQ.SelfID))
	runtime := New(ctx, normalizer, working, deliberation.NewAdapter(contextService, planner), nil, nil, executor, Config{SelfID: cfg.QQ.SelfID})
	defer runtime.Close()

	payload := []byte(`{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"m-low","message":[{"type":"text","data":{"text":"普通闲聊"}}]}`)
	outcome, err := runtime.ProcessRawEvent(ctx, payload)
	if err != nil {
		t.Fatalf("process raw event: %v", err)
	}
	if outcome.Decision.Action != policydomain.ActionSilent || outcome.Decision.ReasonCodes[0] != "candidate_below_score" {
		t.Fatalf("expected below-score silence, got %+v", outcome.Decision)
	}
	if outcome.Receipt.Sent {
		t.Fatalf("below-score candidate sent unexpectedly: %+v", outcome.Receipt)
	}
}

func TestProcessCandidateCompletesAfterDeliberationError(t *testing.T) {
	ctx := context.Background()
	working := group_actor.NewManager(ingress.NewMemoryEventLog())
	defer working.Close()

	at := time.Now()
	record := humandomain.EventRecord{
		EventID: "error-event", GroupID: 7, UserID: 9, Origin: humandomain.OriginInbound, Timestamp: at,
		Event: conversationdomain.ConversationEvent{EventID: "error-event", GroupID: 7, UserID: 9, Kind: conversationdomain.EventMessage, Text: "hello", TimestampUnix: at.Unix()},
	}
	_, err := working.Observe(ctx, record)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	candidateInput := humandomain.ThoughtCandidate{
		CandidateID: "error-candidate", SourceEventIDs: []string{"error-event"}, Intent: "answer",
		Urgency: 1, Score: 1, DueAt: at.Add(-time.Second), ExpiresAt: at.Add(time.Minute), Status: humandomain.CandidatePending,
	}
	if err := working.EnqueueCandidate(ctx, 7, candidateInput); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	candidate, ok, err := working.ClaimDue(ctx, 7, time.Now(), 0)
	if err != nil || !ok {
		t.Fatalf("claim: candidate=%+v ok=%v err=%v", candidate, ok, err)
	}
	runtime := &Runtime{working: working, deliberator: failingDeliberator{}}
	if _, err := runtime.processCandidate(ctx, 7, candidate); err == nil {
		t.Fatal("expected deliberation error")
	}
	final, err := working.Snapshot(ctx, 7)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, item := range final.Candidates {
		if item.CandidateID == candidate.CandidateID && item.Status != humandomain.CandidateCompleted {
			t.Fatalf("candidate was not completed after error: %+v", item)
		}
	}
}
