package action

import (
	"context"
	"fmt"
	"strconv"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
)

type Service struct {
	sender ports.OutboundSender
	memes  *memesvc.Service
}

func New(sender ports.OutboundSender, memes *memesvc.Service) *Service {
	return &Service{sender: sender, memes: memes}
}

func (s *Service) Execute(ctx context.Context, event conversationdomain.ConversationEvent, decision policydomain.AutonomyDecision, plan replydomain.ReplyPlan) (replydomain.ActionReceipt, error) {
	if decision.Action == policydomain.ActionSilent {
		return replydomain.ActionReceipt{
			ActionID: decision.DecisionID,
			Sent:     false,
		}, nil
	}

	action, err := s.buildAction(ctx, event, decision, plan)
	if err != nil {
		return replydomain.ActionReceipt{}, err
	}

	return s.sender.Send(ctx, action)
}

func (s *Service) buildAction(ctx context.Context, event conversationdomain.ConversationEvent, decision policydomain.AutonomyDecision, plan replydomain.ReplyPlan) (replydomain.ActionExecution, error) {
	switch decision.Action {
	case policydomain.ActionMemeOnly:
		if s.memes == nil {
			return replydomain.ActionExecution{}, fmt.Errorf("meme service unavailable")
		}
		memeID, _ := plan.ActionParams["meme_id"].(string)
		replyTo, _ := plan.ActionParams["reply_to_message_id"].(string)
		caption, _ := plan.ActionParams["caption"].(string)
		segments, err := s.memes.BuildSendSegments(ctx, memeID, replyTo, caption)
		if err != nil {
			return replydomain.ActionExecution{}, err
		}
		return replydomain.ActionExecution{
			ActionID:    fmt.Sprintf("%s-action", decision.DecisionID),
			Kind:        policydomain.ActionMemeOnly,
			GroupID:     event.GroupID,
			Segments:    segments,
			ReasonCodes: decision.ReasonCodes,
			Meta:        map[string]any{"send_mode": plan.SendMode},
		}, nil
	case policydomain.ActionRecall:
		messageID, _ := plan.ActionParams["message_id"].(string)
		return replydomain.ActionExecution{
			ActionID:        fmt.Sprintf("%s-action", decision.DecisionID),
			Kind:            policydomain.ActionRecall,
			GroupID:         event.GroupID,
			TargetMessageID: messageID,
			ReasonCodes:     decision.ReasonCodes,
			Meta:            map[string]any{"send_mode": plan.SendMode},
		}, nil
	case policydomain.ActionPokeBack:
		userID := int64From(plan.ActionParams["user_id"])
		return replydomain.ActionExecution{
			ActionID:     fmt.Sprintf("%s-action", decision.DecisionID),
			Kind:         policydomain.ActionPokeBack,
			GroupID:      event.GroupID,
			TargetUserID: userID,
			ReasonCodes:  decision.ReasonCodes,
			Meta:         map[string]any{"send_mode": plan.SendMode},
		}, nil
	case policydomain.ActionReact:
		messageID, _ := plan.ActionParams["message_id"].(string)
		emojiID, _ := plan.ActionParams["emoji_id"].(string)
		if messageID == "" {
			messageID = event.MessageID
		}
		return replydomain.ActionExecution{
			ActionID:        fmt.Sprintf("%s-action", decision.DecisionID),
			Kind:            policydomain.ActionReact,
			GroupID:         event.GroupID,
			TargetMessageID: messageID,
			ReasonCodes:     decision.ReasonCodes,
			Meta:            map[string]any{"send_mode": plan.SendMode, "emoji_id": emojiID},
		}, nil
	default:
		segments := make([]conversationdomain.MessageSegment, 0, len(plan.Bubbles)+1)
		if plan.ReplyToMessageID != "" {
			segments = append(segments, conversationdomain.MessageSegment{
				Type: "reply",
				Data: map[string]any{"id": plan.ReplyToMessageID},
			})
		}
		for _, bubble := range plan.Bubbles {
			if bubble == "" {
				continue
			}
			segments = append(segments, conversationdomain.MessageSegment{
				Type: "text",
				Data: map[string]any{"text": bubble},
			})
		}
		if len(segments) == 0 && plan.FallbackText != "" {
			segments = append(segments, conversationdomain.MessageSegment{
				Type: "text",
				Data: map[string]any{"text": plan.FallbackText},
			})
		}
		return replydomain.ActionExecution{
			ActionID:         fmt.Sprintf("%s-action", decision.DecisionID),
			Kind:             policydomain.ActionReply,
			GroupID:          event.GroupID,
			ReplyToMessageID: plan.ReplyToMessageID,
			Segments:         segments,
			ReasonCodes:      decision.ReasonCodes,
			Meta: map[string]any{
				"send_mode": plan.SendMode,
			},
		}, nil
	}
}

func int64From(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	default:
		return 0
	}
}
