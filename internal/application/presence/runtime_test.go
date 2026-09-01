package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	"github.com/phlin/go-agent/internal/application/action"
	contextsvc "github.com/phlin/go-agent/internal/application/context"
	"github.com/phlin/go-agent/internal/application/normalizer"
	personasvc "github.com/phlin/go-agent/internal/application/persona"
	policysvc "github.com/phlin/go-agent/internal/application/policy"
	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/application/presence/deliberation"
	"github.com/phlin/go-agent/internal/application/presence/group_actor"
	"github.com/phlin/go-agent/internal/application/presence/ingress"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	"github.com/phlin/go-agent/internal/testsupport"
)

type failingDeliberator struct{}

func (failingDeliberator) Deliberate(context.Context, deliberation.Input) (deliberation.Result, error) {
	return deliberation.Result{}, errors.New("deliberation failed")
}

// recordingPlanner 记录 Plan 被调用,返回固定回复——测试"消息走到 planner 并被执行",
// 不依赖确定性 planner 的话术内容。
type recordingPlanner struct{ calls int }

func (p *recordingPlanner) Plan(_ context.Context, _ conversationdomain.ContextSnapshot, _ policydomain.AutonomyDecision) (replydomain.ReplyPlan, error) {
	p.calls++
	return replydomain.ReplyPlan{Bubbles: []string{"planner considered this"}, PlannedActions: []policydomain.DecisionAction{policydomain.ActionReply}, SendMode: "group"}, nil
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

type canonDeliberator struct{}

func (canonDeliberator) Deliberate(_ context.Context, input deliberation.Input) (deliberation.Result, error) {
	return deliberation.Result{
		Snapshot: conversationdomain.ContextSnapshot{Event: input.Envelope.Event},
		Decision: policydomain.AutonomyDecision{DecisionID: "canon-decision", Action: policydomain.ActionReply},
		Plan: replydomain.ReplyPlan{
			Bubbles:        []string{"我高中读的是文科。"},
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionReply},
			SendMode:       "group",
			ProposedPersonaFacts: []replydomain.PersonaFactCandidate{{
				Key: "education.high_school_major", Value: "文科", EvidenceText: "我高中读的是文科",
			}},
		},
	}, nil
}

type runtimeCanonStore struct {
	facts []personadomain.PersonaFact
}

func (s *runtimeCanonStore) AppendPersonaFact(_ context.Context, fact personadomain.PersonaFact) error {
	s.facts = append(s.facts, fact)
	return nil
}

func (s *runtimeCanonStore) CurrentPersonaFacts(_ context.Context, _ string, _ time.Time) ([]personadomain.PersonaFact, error) {
	return append([]personadomain.PersonaFact(nil), s.facts...), nil
}

type failedSender struct{}

func (failedSender) Send(context.Context, replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	return replydomain.ActionReceipt{}, errors.New("send failed")
}

type secondSendFails struct{ calls int }

func (s *secondSendFails) Send(_ context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	s.calls++
	if s.calls == 2 {
		return replydomain.ActionReceipt{}, errors.New("second send failed")
	}
	return replydomain.ActionReceipt{ActionID: action.ActionID, PlatformMessageID: "partial-1", Sent: true}, nil
}

type partialCanonDeliberator struct{}

func (partialCanonDeliberator) Deliberate(_ context.Context, input deliberation.Input) (deliberation.Result, error) {
	result, _ := (canonDeliberator{}).Deliberate(context.Background(), input)
	result.Plan.Bubbles = []string{"我高中读的是文科。", "这事我记得很清楚。"}
	return result, nil
}

type blockingTurnObserver struct{ checks int }

func (o *blockingTurnObserver) CanDeliberate(context.Context, int64, time.Time) (bool, error) {
	o.checks++
	return false, nil
}

func (*blockingTurnObserver) AfterTurn(context.Context, conversationdomain.ContextSnapshot, policydomain.AutonomyDecision, replydomain.ActionReceipt) error {
	return nil
}

type inboundResetTurnObserver struct {
	checks int
	reset  bool
}

func (o *inboundResetTurnObserver) ObserveInbound(context.Context, conversationdomain.ConversationEvent) error {
	o.reset = true
	return nil
}

func (o *inboundResetTurnObserver) CanDeliberate(context.Context, int64, time.Time) (bool, error) {
	o.checks++
	return o.reset, nil
}

func (*inboundResetTurnObserver) AfterTurn(context.Context, conversationdomain.ContextSnapshot, policydomain.AutonomyDecision, replydomain.ActionReceipt) error {
	return nil
}

func TestProcessRawEventUsesCandidateRuntime(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.QQ.SelfID = 123456
	db := testsupport.NewDB(t)
	store := postgresstore.NewStore(db)
	states := postgresstore.NewStateStore(db)
	policy := policysvc.New(cfg)
	normalizer := normalizer.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)
	eventLog := ingress.NewMemoryEventLog()
	working := group_actor.NewManager(eventLog)
	defer working.Close()
	retriever := retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{})
	contextService := contextsvc.New(store, store, states, policy, cfg.Persona, retriever, cfg.Memory.TopK)
	contextService.WithWorkingMemory(working)
	// 用 recordingDeliberator 断言消息走到 deliberation 并被执行,
	// 不依赖确定性 planner 的话术内容
	planner := &recordingPlanner{}
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
	db := testsupport.NewDB(t)
	store := postgresstore.NewStore(db)
	states := postgresstore.NewStateStore(db)
	policy := policysvc.New(cfg)
	normalizer := normalizer.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)
	eventLog := ingress.NewMemoryEventLog()
	working := group_actor.NewManager(eventLog)
	defer working.Close()
	retriever := retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{})
	contextService := contextsvc.New(store, store, states, policy, cfg.Persona, retriever, cfg.Memory.TopK)
	contextService.WithWorkingMemory(working)
	planner := &recordingPlanner{}
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

func TestInboundMessageResetsConsecutiveTurnGate(t *testing.T) {
	ctx := context.Background()
	working := group_actor.NewManager(ingress.NewMemoryEventLog())
	defer working.Close()
	planner := &recordingDeliberator{}
	turns := &inboundResetTurnObserver{}
	runtime := New(ctx, normalizer.New("onebot", 123456, nil), working, planner, nil, turns,
		action.New(inmemory.NewSender(), nil, nil), Config{SelfID: 123456})
	defer runtime.Close()

	payload := []byte(`{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"m-reset","message":[{"type":"text","data":{"text":"普通闲聊"}}]}`)
	outcome, err := runtime.ProcessRawEvent(ctx, payload)
	if err != nil {
		t.Fatalf("process raw event: %v", err)
	}
	if !turns.reset || turns.checks != 1 {
		t.Fatalf("inbound reset hook was not applied: %+v", turns)
	}
	if !outcome.Receipt.Sent || outcome.Decision.Action != policydomain.ActionReply {
		t.Fatalf("inbound message remained blocked after reset: %+v", outcome)
	}
}

func TestPersonaCanonPersistsOnlyAfterSuccessfulSend(t *testing.T) {
	for _, test := range []struct {
		name      string
		sender    ports.OutboundSender
		wantFacts int
		wantError bool
	}{
		{name: "success", sender: inmemory.NewSender(), wantFacts: 1},
		{name: "failure", sender: failedSender{}, wantFacts: 0, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			working := group_actor.NewManager(ingress.NewMemoryEventLog())
			defer working.Close()
			store := &runtimeCanonStore{}
			runtime := New(ctx, normalizer.New("onebot", 123456, nil), working, canonDeliberator{}, nil, nil,
				action.New(test.sender, nil, nil), Config{SelfID: 123456})
			defer runtime.Close()
			runtime.SetCanonService(personasvc.NewCanonService(store, runtimeCanonDefinition(t), nil))

			payload := []byte(`{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"m-canon","message":[{"type":"text","data":{"text":"你高中学什么？"}}]}`)
			_, err := runtime.ProcessRawEvent(ctx, payload)
			if (err != nil) != test.wantError {
				t.Fatalf("ProcessRawEvent error=%v wantError=%v", err, test.wantError)
			}
			if len(store.facts) != test.wantFacts {
				t.Fatalf("stored facts=%d want=%d: %+v", len(store.facts), test.wantFacts, store.facts)
			}
		})
	}
}

func TestPersonaCanonUsesSuccessfullyDeliveredPartialText(t *testing.T) {
	ctx := context.Background()
	working := group_actor.NewManager(ingress.NewMemoryEventLog())
	defer working.Close()
	store := &runtimeCanonStore{}
	runtime := New(ctx, normalizer.New("onebot", 123456, nil), working, partialCanonDeliberator{}, nil, nil,
		action.New(&secondSendFails{}, nil, nil), Config{SelfID: 123456})
	defer runtime.Close()
	runtime.SetCanonService(personasvc.NewCanonService(store, runtimeCanonDefinition(t), nil))

	payload := []byte(`{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"m-partial","message":[{"type":"text","data":{"text":"你高中学什么？"}}]}`)
	outcome, err := runtime.ProcessRawEvent(ctx, payload)
	if err == nil {
		t.Fatal("expected second bubble send error")
	}
	if !outcome.Receipt.Sent || outcome.Receipt.DeliveredText != "我高中读的是文科。" {
		t.Fatalf("actual partial delivery was not preserved: %+v", outcome.Receipt)
	}
	if len(store.facts) != 1 || store.facts[0].Value != "文科" {
		t.Fatalf("fact from delivered first bubble was not persisted: %+v", store.facts)
	}
}

func runtimeCanonDefinition(t *testing.T) personadomain.PersonaDefinition {
	t.Helper()
	definition, err := personadomain.Compile(personadomain.PersonaConfig{
		ID: "main", Name: "Test", Facts: []personadomain.PersonaFactDefinition{
			{Key: "identity.display_name", Value: "Test", Policy: personadomain.FactPolicyLocked},
			{Key: "education.high_school_major", Policy: personadomain.FactPolicySelfCompleteOnce},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
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
	candidate, ok, err := working.ClaimDue(ctx, 7, time.Now())
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
