package runtime

import (
	"context"
	"testing"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	"github.com/phlin/go-agent/internal/humanbot/runtime/deliberation"
	"github.com/phlin/go-agent/internal/humanbot/runtime/group_actor"
	"github.com/phlin/go-agent/internal/humanbot/runtime/ingress"
	"github.com/phlin/go-agent/internal/services/action"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
	"github.com/phlin/go-agent/internal/services/normalizer"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
	promptingsvc "github.com/phlin/go-agent/internal/services/prompting"
)

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
	runtime := New(ctx, normalizer, working, deliberation.NewAdapter(contextService, planner), executor, Config{SelfID: cfg.QQ.SelfID})
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
