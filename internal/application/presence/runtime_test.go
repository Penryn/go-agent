package presence

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	workspaceapp "github.com/phlin/go-agent/internal/application/workspace"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	"github.com/phlin/go-agent/internal/testsupport"
)

func TestPrepareWorkspaceStagesInboundAttachment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("attachment-content"))
	}))
	defer server.Close()
	manager, err := workspaceapp.NewManager(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{deps: Dependencies{Workspaces: manager}}
	task, err := runtime.prepareWorkspace(context.Background(), conversationdomain.EventEnvelope{
		Event: conversationdomain.ConversationEvent{
			EventID: "event-1",
			Attachments: []mediadomain.MultimodalAttachment{{
				AttachmentID: "attachment-1",
				Kind:         mediadomain.MediaFile,
				URL:          server.URL + "/input.zip",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Cleanup(task.Workspace)
	if len(task.InputFiles) != 1 || task.InputFiles[0] != "./input/input.zip" {
		t.Fatalf("unexpected input files: %#v", task.InputFiles)
	}
	content, err := os.ReadFile(filepath.Join(task.Workspace.Root, task.InputFiles[0][2:]))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "attachment-content" {
		t.Fatalf("unexpected staged content %q", content)
	}
}

// recordingPlanner 记录 Plan 被调用,返回固定回复——测试"消息走到 planner 并被执行",
// 不依赖确定性 planner 的话术内容。
type recordingPlanner struct{ calls int }

func (p *recordingPlanner) Plan(_ context.Context, _ conversationdomain.ContextSnapshot, _ policydomain.AutonomyDecision) (replydomain.ReplyPlan, error) {
	p.calls++
	return replydomain.ReplyPlan{Bubbles: []string{"planner considered this"}, PlannedActions: []policydomain.DecisionAction{policydomain.ActionReply}, SendMode: "group"}, nil
}

type recordingDeliberator struct{ calls int }

func (d *recordingDeliberator) Deliberate(_ context.Context, envelope conversationdomain.EventEnvelope) (deliberation.Result, error) {
	d.calls++
	return deliberation.Result{
		Snapshot: conversationdomain.ContextSnapshot{Event: envelope.Event},
		Decision: policydomain.AutonomyDecision{DecisionID: "model-decision", Action: policydomain.ActionReply},
		Plan:     replydomain.ReplyPlan{Bubbles: []string{"model considered this"}, PlannedActions: []policydomain.DecisionAction{policydomain.ActionReply}, SendMode: "group"},
	}, nil
}

type canonDeliberator struct{}

func (canonDeliberator) Deliberate(_ context.Context, envelope conversationdomain.EventEnvelope) (deliberation.Result, error) {
	return deliberation.Result{
		Snapshot: conversationdomain.ContextSnapshot{Event: envelope.Event},
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
	facts     []personadomain.PersonaFact
	appendErr error
}

func (s *runtimeCanonStore) AppendPersonaFact(_ context.Context, fact personadomain.PersonaFact) error {
	if s.appendErr != nil {
		return s.appendErr
	}
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

func (partialCanonDeliberator) Deliberate(_ context.Context, envelope conversationdomain.EventEnvelope) (deliberation.Result, error) {
	result, _ := (canonDeliberator{}).Deliberate(context.Background(), envelope)
	result.Plan.Bubbles = []string{"我高中读的是文科。", "这事我记得很清楚。"}
	return result, nil
}

type blockingTurnObserver struct{ checks int }

func (*blockingTurnObserver) ObserveInbound(context.Context, conversationdomain.ConversationEvent) error {
	return nil
}

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

type lifecycleTurnObserver struct {
	afterCalls int
	afterErr   error
}

func (*lifecycleTurnObserver) ObserveInbound(context.Context, conversationdomain.ConversationEvent) error {
	return nil
}

func (*lifecycleTurnObserver) CanDeliberate(context.Context, int64, time.Time) (bool, error) {
	return true, nil
}

func (o *lifecycleTurnObserver) AfterTurn(context.Context, conversationdomain.ContextSnapshot, policydomain.AutonomyDecision, replydomain.ActionReceipt) error {
	o.afterCalls++
	return o.afterErr
}

type lifecycleFeedbackObserver struct {
	sentCalls int
	sentErr   error
}

func (*lifecycleFeedbackObserver) ObserveInbound(context.Context, conversationdomain.ConversationEvent) error {
	return nil
}

func (o *lifecycleFeedbackObserver) ObserveSent(context.Context, conversationdomain.ConversationEvent, replydomain.ActionReceipt, string) error {
	o.sentCalls++
	return o.sentErr
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
	working := group_actor.NewManager()
	defer working.Close()
	retriever := retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{})
	contextService := contextsvc.New(store, store, states, policy, cfg.Persona, retriever, cfg.Memory.TopK)
	contextService.WithWorkingMemory(working)
	// 用 recordingDeliberator 断言消息走到 deliberation 并被执行,
	// 不依赖确定性 planner 的话术内容
	planner := &recordingPlanner{}
	sender := inmemory.NewSender()
	executor := action.New(sender, nil, nil, action.WithPresenceObserver(working), action.WithSelfID(cfg.QQ.SelfID))
	runtime := New(ctx, Dependencies{Normalizer: normalizer, WorkingMemory: working, Deliberator: deliberation.NewAdapter(contextService, planner), Executor: executor}, Config{})
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

func TestProcessRawEventIgnoresMetaAndInvalidGroups(t *testing.T) {
	ctx := context.Background()
	working := group_actor.NewManager()
	defer working.Close()
	runtime := New(ctx, Dependencies{Normalizer: normalizer.New("onebot", 123456, nil), WorkingMemory: working, Executor: action.New(inmemory.NewSender(), nil, nil)}, Config{})
	defer runtime.Close()

	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "meta event",
			payload: `{"post_type":"meta_event","meta_event_type":"heartbeat","time":1710000000,"group_id":100,"user_id":200}`,
		},
		{
			name:    "invalid group",
			payload: `{"post_type":"message","message_type":"private","time":1710000000,"group_id":0,"user_id":200,"message_id":"private-1","message":[{"type":"text","data":{"text":"hello"}}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, err := runtime.ProcessRawEvent(ctx, []byte(tt.payload))
			if err != nil {
				t.Fatalf("process raw event: %v", err)
			}
			if outcome.Decision.Action != policydomain.ActionSilent || outcome.Receipt.Sent {
				t.Fatalf("event was not ignored: %+v", outcome)
			}
		})
	}
	if ids := working.GroupIDs(); len(ids) != 0 {
		t.Fatalf("ignored events created working groups: %v", ids)
	}
}

func TestRateLimitAppliesBeforeModelDeliberation(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.QQ.SelfID = 123456
	working := group_actor.NewManager()
	defer working.Close()
	planner := &recordingDeliberator{}
	turns := &blockingTurnObserver{}
	runtime := New(ctx, Dependencies{Normalizer: normalizer.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases), WorkingMemory: working, Deliberator: planner, Turns: turns, Executor: action.New(inmemory.NewSender(), nil, nil)}, Config{})
	defer runtime.Close()

	payload := []byte(`{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"m-rate","message":[{"type":"at","data":{"qq":"123456"}},{"type":"text","data":{"text":"帮我看看"}}]}`)
	outcome, err := runtime.ProcessRawEvent(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if planner.calls != 0 || turns.checks != 1 {
		t.Fatalf("rate limit was not enforced before model: model=%d checks=%d", planner.calls, turns.checks)
	}
	if outcome.Decision.Action != policydomain.ActionSilent || outcome.Receipt.Sent {
		t.Fatalf("rate limit did not suppress only output: %+v", outcome)
	}
	memory, err := working.Snapshot(ctx, 100)
	if err != nil || len(memory.RecentTail) != 1 || memory.RecentTail[0].Event.MessageID != "m-rate" {
		t.Fatalf("turn gate lost the inbound fact: memory=%+v err=%v", memory, err)
	}
}

func TestInboundMessageResetsConsecutiveTurnGate(t *testing.T) {
	ctx := context.Background()
	working := group_actor.NewManager()
	defer working.Close()
	planner := &recordingDeliberator{}
	turns := &inboundResetTurnObserver{}
	runtime := New(ctx, Dependencies{Normalizer: normalizer.New("onebot", 123456, nil), WorkingMemory: working, Deliberator: planner, Turns: turns, Executor: action.New(inmemory.NewSender(), nil, nil)}, Config{})
	defer runtime.Close()

	payload := []byte(`{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"m-reset","message":[{"type":"at","data":{"qq":"123456"}},{"type":"text","data":{"text":"帮我看看"}}]}`)
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
			working := group_actor.NewManager()
			defer working.Close()
			store := &runtimeCanonStore{}
			runtime := New(ctx, Dependencies{Normalizer: normalizer.New("onebot", 123456, nil), WorkingMemory: working, Deliberator: canonDeliberator{}, Executor: action.New(test.sender, nil, nil)}, Config{})
			defer runtime.Close()
			runtime.deps.Canon = personasvc.NewCanonService(store, runtimeCanonDefinition(t), nil)

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
	working := group_actor.NewManager()
	defer working.Close()
	store := &runtimeCanonStore{}
	runtime := New(ctx, Dependencies{Normalizer: normalizer.New("onebot", 123456, nil), WorkingMemory: working, Deliberator: partialCanonDeliberator{}, Executor: action.New(&secondSendFails{}, nil, nil)}, Config{})
	defer runtime.Close()
	runtime.deps.Canon = personasvc.NewCanonService(store, runtimeCanonDefinition(t), nil)

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

func TestPostSendFeedbackFailureDoesNotSkipTurnObserver(t *testing.T) {
	ctx := context.Background()
	working := group_actor.NewManager()
	defer working.Close()
	turns := &lifecycleTurnObserver{}
	feedback := &lifecycleFeedbackObserver{sentErr: errors.New("feedback unavailable")}
	runtime := New(ctx, Dependencies{Normalizer: normalizer.New("onebot", 123456, nil), WorkingMemory: working, Deliberator: &recordingDeliberator{}, Turns: turns, Executor: action.New(inmemory.NewSender(), nil, nil)}, Config{})
	defer runtime.Close()
	runtime.deps.Feedback = feedback

	payload := []byte(`{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"m-feedback-fail","message":[{"type":"text","data":{"text":"hello"}}]}`)
	outcome, err := runtime.ProcessRawEvent(ctx, payload)
	if err == nil || !errors.Is(err, feedback.sentErr) {
		t.Fatalf("expected feedback error, got %v", err)
	}
	if !outcome.Receipt.Sent || feedback.sentCalls != 1 || turns.afterCalls != 1 {
		t.Fatalf("post-send lifecycle was short-circuited: receipt=%+v feedback=%d turns=%d", outcome.Receipt, feedback.sentCalls, turns.afterCalls)
	}
}

func TestPostSendCanonFailureDoesNotSkipTurnObserver(t *testing.T) {
	ctx := context.Background()
	working := group_actor.NewManager()
	defer working.Close()
	turns := &lifecycleTurnObserver{}
	canonErr := errors.New("canon unavailable")
	store := &runtimeCanonStore{appendErr: canonErr}
	runtime := New(ctx, Dependencies{Normalizer: normalizer.New("onebot", 123456, nil), WorkingMemory: working, Deliberator: canonDeliberator{}, Turns: turns, Executor: action.New(inmemory.NewSender(), nil, nil)}, Config{})
	defer runtime.Close()
	runtime.deps.Canon = personasvc.NewCanonService(store, runtimeCanonDefinition(t), nil)

	payload := []byte(`{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"m-canon-fail","message":[{"type":"text","data":{"text":"你高中学什么？"}}]}`)
	outcome, err := runtime.ProcessRawEvent(ctx, payload)
	if err == nil || !errors.Is(err, canonErr) {
		t.Fatalf("expected canon error, got %v", err)
	}
	if !outcome.Receipt.Sent || turns.afterCalls != 1 {
		t.Fatalf("turn observer was skipped after canon failure: receipt=%+v turns=%d", outcome.Receipt, turns.afterCalls)
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
