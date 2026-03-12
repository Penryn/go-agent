package gate

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

type Decision struct {
	CueBot      bool    `json:"cue_bot"`
	NaturalHook bool    `json:"natural_hook"`
	Score       float64 `json:"score"`
	Reason      string  `json:"reason"`
}

type Service struct {
	factory ports.ChatModelFactory
}

func New(factory ports.ChatModelFactory) *Service {
	return &Service{factory: factory}
}

func (s *Service) Evaluate(ctx context.Context, snapshot conversationdomain.ContextSnapshot) (Decision, error) {
	chatModel, err := s.factory.GateChatModel(ctx)
	if err != nil || chatModel == nil {
		return heuristic(snapshot), nil
	}

	msg, err := chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage("You are a QQ group gate model. Output strict JSON with cue_bot,natural_hook,score,reason."),
		schema.UserMessage("Event: " + snapshot.Event.Text),
	})
	if err != nil {
		return heuristic(snapshot), nil
	}

	var decision Decision
	if err := json.Unmarshal([]byte(msg.Content), &decision); err != nil {
		return heuristic(snapshot), nil
	}
	return decision, nil
}

func heuristic(snapshot conversationdomain.ContextSnapshot) Decision {
	text := strings.TrimSpace(snapshot.Event.Text)
	switch {
	case snapshot.Event.MentionedBot || snapshot.Event.NamedBot || snapshot.Event.IsReplyToBot:
		return Decision{CueBot: true, NaturalHook: true, Score: 1, Reason: "direct_triggered"}
	case len(snapshot.Event.Attachments) > 0:
		return Decision{NaturalHook: true, Score: 0.7, Reason: "media_hook"}
	case strings.Contains(text, "?") || strings.Contains(text, "？"):
		return Decision{NaturalHook: true, Score: 0.6, Reason: "question_hook"}
	default:
		return Decision{Score: 0.1, Reason: "low_signal"}
	}
}
