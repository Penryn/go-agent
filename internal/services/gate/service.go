package gate

import (
	"context"
	"encoding/json"
	"fmt"
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

// buildGateContext 将 RecentTurns 最近 5 条格式化为上下文字符串，
// 并在同一用户 30s 内连续发言时追加 [连续发言] 标注，辅助 Gate 模型判断碎片消息语义。
func buildGateContext(snapshot conversationdomain.ContextSnapshot) string {
	turns := snapshot.RecentTurns
	if len(turns) > 5 {
		turns = turns[len(turns)-5:]
	}

	var sb strings.Builder
	if len(turns) > 0 {
		sb.WriteString("Recent context:\n")
		baseTS := snapshot.Event.TimestampUnix
		sameUserCount := 0
		for _, t := range turns {
			if strings.TrimSpace(t.Text) == "" {
				continue
			}
			var timeTag string
			if baseTS > 0 && t.TimestampUnix > 0 {
				diff := baseTS - t.TimestampUnix
				if diff < 0 {
					diff = 0
				}
				timeTag = fmt.Sprintf("[T-%ds]", diff)
			}
			sb.WriteString(fmt.Sprintf("%s[user_%d] %s\n", timeTag, t.UserID, strings.TrimSpace(t.Text)))
			if t.UserID == snapshot.Event.UserID && baseTS-t.TimestampUnix <= 30 {
				sameUserCount++
			}
		}
		if sameUserCount >= 2 {
			sb.WriteString("[连续发言：以上为同一用户短时间内的分条发送，请合并理解为完整语义]\n")
		}
	}

	sb.WriteString("Current message: " + snapshot.Event.Text)
	return sb.String()
}

func (s *Service) Evaluate(ctx context.Context, snapshot conversationdomain.ContextSnapshot) (Decision, error) {
	chatModel, err := s.factory.GateChatModel(ctx)
	if err != nil || chatModel == nil {
		return heuristic(snapshot), nil
	}

	msg, err := chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage("You are a QQ group gate model. Analyze the full conversation window — the trigger message may only make sense in context of prior messages. Output strict JSON with cue_bot,natural_hook,score,reason."),
		schema.UserMessage(buildGateContext(snapshot)),
	})
	if err != nil {
		return heuristic(snapshot), nil
	}

	var decision Decision
	if err := json.Unmarshal([]byte(msg.Content), &decision); err != nil {
		return heuristic(snapshot), nil
	}
	if decision.Score < 0 {
		decision.Score = 0
	} else if decision.Score > 1 {
		decision.Score = 1
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
