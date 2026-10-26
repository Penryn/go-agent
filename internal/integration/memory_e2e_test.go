package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	"github.com/phlin/go-agent/internal/application/action"
	contextsvc "github.com/phlin/go-agent/internal/application/context"
	"github.com/phlin/go-agent/internal/application/learning"
	memsvc "github.com/phlin/go-agent/internal/application/memory"
	"github.com/phlin/go-agent/internal/application/normalizer"
	policysvc "github.com/phlin/go-agent/internal/application/policy"
	"github.com/phlin/go-agent/internal/application/ports"
	presenceruntime "github.com/phlin/go-agent/internal/application/presence"
	"github.com/phlin/go-agent/internal/application/presence/deliberation"
	groupactor "github.com/phlin/go-agent/internal/application/presence/group_actor"
	"github.com/phlin/go-agent/internal/application/retrieval"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	"github.com/phlin/go-agent/internal/testsupport"
)

type preferenceExtractor struct{}

func (preferenceExtractor) Generate(context.Context, string) (string, error) {
	return `[{"subject_kind":"user","subject_id":"7","type":"social","content":"用户7不希望被戳","predicate":"allow_poke","normalized_value":"false","evidence_event_ids":["0"],"confidence":0.95}]`, nil
}

type pokePlanner struct{}

func (pokePlanner) Plan(context.Context, conversationdomain.ContextSnapshot, policydomain.AutonomyDecision) (replydomain.ReplyPlan, error) {
	return replydomain.ReplyPlan{PlannedActions: []policydomain.DecisionAction{policydomain.ActionPokeBack}, ActionParams: map[string]any{"user_id": int64(7)}}, nil
}

func TestLearnedPreferenceBlocksActualDelivery(t *testing.T) {
	ctx := context.Background()
	db := testsupport.NewDB(t)
	store := postgresstore.NewStore(db)
	event := conversationdomain.ConversationEvent{EventID: "preference", GroupID: 1, UserID: 7, Text: "以后别戳我", TimestampUnix: time.Now().Unix()}
	if err := store.ArchiveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	learner, err := learning.New(ctx, store, store, memsvc.New(store), learning.WithLLM(preferenceExtractor{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := learner.ProcessBatch(ctx, 1, []string{event.EventID}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	working := groupactor.NewManager(groupactor.WithArchive(store), groupactor.WithStateStore(store))
	defer working.Close()
	contexts := contextsvc.New(store, store, postgresstore.NewStateStore(db), policysvc.New(cfg), cfg.Persona, nil, 6)
	contexts.WithWorkingMemory(working)
	contexts.WithConstraints(store)
	sender := inmemory.NewSender()
	runtime := presenceruntime.New(ctx, presenceruntime.Dependencies{
		Normalizer: normalizer.New("onebot", 99, nil), WorkingMemory: working,
		Deliberator: deliberation.NewAdapter(contexts, pokePlanner{}), Executor: action.New(sender, nil, nil),
	}, presenceruntime.Config{})
	defer runtime.Close()
	payload := []byte(`{"post_type":"notice","notice_type":"notify","sub_type":"poke","self_id":99,"group_id":1,"user_id":7,"target_id":99}`)
	outcome, err := runtime.ProcessRawEvent(ctx, payload)
	if err == nil || outcome.Receipt.DropReason != "interaction_constraint" || outcome.Receipt.Sent || len(sender.Actions()) != 0 {
		t.Fatalf("learned restriction failed at the delivery boundary: receipt=%+v err=%v", outcome.Receipt, err)
	}
	if len(outcome.Snapshot.MemoryConstraints) != 1 || outcome.Snapshot.MemoryConstraints[0].Value != "false" {
		t.Fatalf("learned preference was absent from model snapshot: %+v", outcome.Snapshot.MemoryConstraints)
	}
}

func TestExplicitMemoryIsRetrievableOnlyWithinItsVisibility(t *testing.T) {
	ctx := context.Background()
	store := testsupport.NewStore(t)
	memory, err := memsvc.New(store).RememberExplicit(ctx, memsvc.ExplicitMemoryInput{GroupID: 1, UserID: 7, Content: "喜欢咖啡", SourceEventID: "evidence"})
	if err != nil {
		t.Fatal(err)
	}
	retriever := retrieval.New(store, store, nil, nil, retrieval.Config{})
	visible, err := retriever.SearchMemories(ctx, ports.MemoryQuery{GroupID: 1, UserID: 7, Query: "咖啡", TopK: 6})
	if err != nil || len(visible) != 1 || visible[0].MemoryID != memory.MemoryID || visible[0].SourceEventID != "evidence" {
		t.Fatalf("explicit memory did not reach production retrieval: %+v err=%v", visible, err)
	}
	hidden, err := retriever.SearchMemories(ctx, ports.MemoryQuery{GroupID: 2, UserID: 7, Query: "咖啡", TopK: 6})
	if err != nil || len(hidden) != 0 {
		t.Fatalf("cross-group memory leaked: %+v err=%v", hidden, err)
	}
}
