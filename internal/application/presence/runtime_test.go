package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/application/action"
	contextsvc "github.com/phlin/go-agent/internal/application/context"
	"github.com/phlin/go-agent/internal/application/normalizer"
	policysvc "github.com/phlin/go-agent/internal/application/policy"
	"github.com/phlin/go-agent/internal/application/presence/deliberation"
	"github.com/phlin/go-agent/internal/application/presence/group_actor"
	"github.com/phlin/go-agent/internal/application/presence/ingress"
	promptingsvc "github.com/phlin/go-agent/internal/application/prompting"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type failingDeliberator struct{}

func (failingDeliberator) Deliberate(context.Context, deliberation.Input) (deliberation.Result, error) {
	return deliberation.Result{}, errors.New("deliberation failed")
}

type recordingDeliberator struct{ calls int }

func (d *recordingDeliberator) Deliberate(_ context.Context, input deliberation.Input) (deliberation.Result, error) {
	d.calls++
	return deliberation.Result{
		Snapshot: conversationdomain.ContextSnapshot{Event: input.Envelope.Event},
		Decision: policydomain.AutonomyDecision{DecisionID: "model-decision", Action: policydomain.ActionReply},
		Plan:     replydomain.ReplyPlan{Bubbles: []string{"model considered this"}, PlannedActions: []policydomain.DecisionAction{policydomain.ActionReply}, SendMode: "group"},
	}, nil
}

type blockingTurnObserver struct{ checks int }

func (o *blockingTurnObserver) CanDeliberate(context.Context, int64, time.Time) (bool, error) {
	o.checks++
	return false, nil
}

func (*blockingTurnObserver) AfterTurn(context.Context, conversationdomain.ContextSnapshot, policydomain.AutonomyDecision, replydomain.ActionReceipt) error {
	return nil
}

func TestProcessRawEventUsesCandidateRuntime(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.QQ.SelfID = 123456
	store := inmemory.NewStore()
	policy := policysvc.New(cfg)
	normalizer := normalizer.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)
	eventLog := ingress.NewMemoryEventLog()
	working := group_actor.NewManager(eventLog)
	defer working.Close()
	retriever := retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{})
	contextService := contextsvc.New(store, store, store, policy, cfg.Persona, retriever, cfg.Memory.TopK)
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

func TestProcessRawEventSendsOrdinaryContentToPlanner(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.QQ.SelfID = 123456
	store := inmemory.NewStore()
	policy := policysvc.New(cfg)
	normalizer := normalizer.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)
	eventLog := ingress.NewMemoryEventLog()
	working := group_actor.NewManager(eventLog)
	defer working.Close()
	retriever := retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{})
	contextService := contextsvc.New(store, store, store, policy, cfg.Persona, retriever, cfg.Memory.TopK)
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
	if outcome.Candidate.CandidateID == "" || outcome.Decision.Action != policydomain.ActionReply {
		t.Fatalf("expected planning for ordinary content, got %+v", outcome)
	}
	if !outcome.Receipt.Sent {
		t.Fatalf("ordinary content did not reach planner: %+v", outcome.Receipt)
	}
}

func TestRateLimitAppliesAfterModelDeliberation(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.QQ.SelfID = 123456
	working := group_actor.NewManager(ingress.NewMemoryEventLog())
	defer working.Close()
	planner := &recordingDeliberator{}
	turns := &blockingTurnObserver{}
	runtime := New(ctx, normalizer.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases), working, planner, nil, turns,
		action.New(inmemory.NewSender(), nil, nil), Config{SelfID: cfg.QQ.SelfID})
	defer runtime.Close()

	payload := []byte(`{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"m-rate","message":[{"type":"text","data":{"text":"普通闲聊"}}]}`)
	outcome, err := runtime.ProcessRawEvent(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if planner.calls != 1 || turns.checks != 1 {
		t.Fatalf("expected one model decision before rate check, model=%d checks=%d", planner.calls, turns.checks)
	}
	if outcome.Decision.Action != policydomain.ActionSilent || outcome.Receipt.Sent {
		t.Fatalf("rate limit did not suppress only output: %+v", outcome)
	}
}

func TestProcessCandidateCompletesAfterDeliberationError(t *testing.T) {
	ctx := context.Background()
	working := group_actor.NewManager(ingress.NewMemoryEventLog())
	defer working.Close()

	at := time.Now()
	record := presencedomain.EventRecord{
		EventID: "error-event", GroupID: 7, UserID: 9, Origin: presencedomain.OriginInbound, Timestamp: at,
		Event: conversationdomain.ConversationEvent{EventID: "error-event", GroupID: 7, UserID: 9, Kind: conversationdomain.EventMessage, Text: "hello", TimestampUnix: at.Unix()},
	}
	_, err := working.Observe(ctx, record)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	candidateInput := presencedomain.ThoughtCandidate{
		CandidateID: "error-candidate", SourceEventIDs: []string{"error-event"}, Intent: "answer",
		Urgency: 1, Score: 1, DueAt: at.Add(-time.Second), ExpiresAt: at.Add(time.Minute), Status: presencedomain.CandidatePending,
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
		if item.CandidateID == candidate.CandidateID && item.Status != presencedomain.CandidateCompleted {
			t.Fatalf("candidate was not completed after error: %+v", item)
		}
	}
}

func TestProactiveCandidateRequiresIdleAndTopic(t *testing.T) {
	ctx := context.Background()
	eventLog := ingress.NewMemoryEventLog()
	working := group_actor.NewManager(eventLog)
	defer working.Close()

	r := &Runtime{
		working:              working,
		proactiveProbability: 1, // 概率必中，聚焦冷场/话题判定
		proactiveThreshold:   0.6,
		lastProactive:        make(map[int64]time.Time),
		ctx:                  ctx,
	}

	// 群完全没动静：无话题不开口
	if _, ok := r.proactiveCandidate(1, time.Now()); ok {
		t.Fatal("empty group must not produce proactive candidate")
	}

	// 有人说话但还在聊（不冷场）：不插嘴
	fresh := time.Now().Add(-time.Minute)
	_, err := working.Observe(ctx, presencedomain.EventRecord{
		EventID: "e1", GroupID: 1, UserID: 7, Origin: presencedomain.OriginInbound,
		Timestamp: fresh,
		Event:     conversationdomain.ConversationEvent{EventID: "e1", GroupID: 1, UserID: 7, Text: "今晚吃什么？", Kind: conversationdomain.EventMessage, TimestampUnix: fresh.Unix()},
	})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if _, ok := r.proactiveCandidate(1, time.Now()); ok {
		t.Fatal("active conversation must not produce proactive candidate")
	}

	// 冷场超过阈值 + 有 OpenLoops（问句）：生成候选
	idle := time.Now()
	candidate, ok := r.proactiveCandidate(1, idle.Add(proactiveIdleThreshold+time.Minute))
	if !ok {
		t.Fatal("idle group with open loops should produce proactive candidate")
	}
	if candidate.Intent != "continue_topic" || candidate.Score < r.proactiveThreshold {
		t.Fatalf("unexpected proactive candidate: %+v", candidate)
	}
	if !candidate.DueAt.After(idle) || candidate.ExpiresAt.Before(candidate.DueAt) {
		t.Fatalf("proactive candidate timing broken: due=%v expires=%v", candidate.DueAt, candidate.ExpiresAt)
	}
}
