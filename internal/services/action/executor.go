package action

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
	outputguardsvc "github.com/phlin/go-agent/internal/services/outputguard"
)

var errDropSend = errors.New("drop send: no text content")
var errGuardSilenced = errors.New("drop send: guard silenced")

type Service struct {
	sender ports.OutboundSender
	memes  *memesvc.Service
	guard  outputguardsvc.Guard // 可为 nil，nil 时跳过清洗
}

func New(sender ports.OutboundSender, memes *memesvc.Service, guard outputguardsvc.Guard) *Service {
	return &Service{sender: sender, memes: memes, guard: guard}
}

func (s *Service) Execute(ctx context.Context, event conversationdomain.ConversationEvent, decision policydomain.AutonomyDecision, plan replydomain.ReplyPlan) (replydomain.ActionReceipt, error) {
	if decision.Action == policydomain.ActionSilent {
		return replydomain.ActionReceipt{
			ActionID:   decision.DecisionID,
			Sent:       false,
			DropReason: "action_silent",
		}, nil
	}

	action, err := s.buildAction(ctx, event, decision, plan)
	if errors.Is(err, errGuardSilenced) {
		return replydomain.ActionReceipt{
			ActionID:   decision.DecisionID,
			Sent:       false,
			DropReason: "guard_silenced",
		}, nil
	}
	if errors.Is(err, errDropSend) {
		return replydomain.ActionReceipt{
			ActionID:   decision.DecisionID,
			Sent:       false,
			DropReason: "no_content",
		}, nil
	}
	if err != nil {
		return replydomain.ActionReceipt{}, err
	}

	// 记录实际发出的 segments，便于排查内容异常
	textContent := ""
	for _, seg := range action.Segments {
		if seg.Type == "text" {
			if t, ok := seg.Data["text"].(string); ok {
				textContent += t
			}
		}
	}
	slog.Info("executor: sending action",
		"action", action.Kind,
		"group_id", action.GroupID,
		"segments", len(action.Segments),
		"reply_to", action.ReplyToMessageID,
		"text", textContent,
	)

	receipt, err := s.sender.Send(ctx, action)
	if err != nil {
		return replydomain.ActionReceipt{}, err
	}

	// B-1: 发送成功后异步标记表情包已发送
	if receipt.Sent && decision.Action == policydomain.ActionMemeOnly {
		if memeID, _ := plan.ActionParams["meme_id"].(string); memeID != "" && s.memes != nil {
			bgCtx := context.WithoutCancel(ctx)
			memes := s.memes
			go func() {
				if markErr := memes.MarkSent(bgCtx, memeID); markErr != nil {
					slog.Warn("executor: MarkSent failed", "meme_id", memeID, "err", markErr)
				}
			}()
		}
	}

	return receipt, nil
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
			// C-4: 表情包获取失败时降级
			slog.Warn("executor: BuildSendSegments failed, attempting fallback",
				"meme_id", memeID, "err", err)
			if plan.FallbackText != "" {
				fallbackSegs := make([]conversationdomain.MessageSegment, 0, 2)
				if replyTo != "" {
					fallbackSegs = append(fallbackSegs, conversationdomain.MessageSegment{
						Type: "reply",
						Data: map[string]any{"id": replyTo},
					})
				}
				fallbackSegs = append(fallbackSegs, conversationdomain.MessageSegment{
					Type: "text",
					Data: map[string]any{"text": plan.FallbackText},
				})
				return replydomain.ActionExecution{
					ActionID:         fmt.Sprintf("%s-action", decision.DecisionID),
					Kind:             policydomain.ActionReply,
					GroupID:          event.GroupID,
					ReplyToMessageID: replyTo,
					Segments:         fallbackSegs,
					ReasonCodes:      decision.ReasonCodes,
					Meta:             map[string]any{"send_mode": plan.SendMode, "meme_fallback": true},
				}, nil
			}
			return replydomain.ActionExecution{}, errDropSend
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
		// OutputGuard 清洗：在组装 segments 前对 bubbles 进行过滤
		bubbles := plan.Bubbles
		if s.guard != nil {
			gr := s.guard.Clean(plan.Bubbles)
			if gr.Suppressed {
				slog.Warn("executor: outputguard suppressed reply",
					"decision_id", decision.DecisionID,
					"reasons", gr.Reasons,
				)
				return replydomain.ActionExecution{}, errGuardSilenced
			}
			if len(gr.Reasons) > 0 {
				slog.Info("executor: outputguard applied rules",
					"decision_id", decision.DecisionID,
					"reasons", gr.Reasons,
				)
			}
			bubbles = gr.Bubbles
		}

		segments := make([]conversationdomain.MessageSegment, 0, len(bubbles)+1)
		if plan.ReplyToMessageID != "" {
			segments = append(segments, conversationdomain.MessageSegment{
				Type: "reply",
				Data: map[string]any{"id": plan.ReplyToMessageID},
			})
		}
		for _, bubble := range bubbles {
			if bubble == "" {
				continue
			}
			segments = append(segments, conversationdomain.MessageSegment{
				Type: "text",
				Data: map[string]any{"text": bubble},
			})
		}
		hasTextSeg := false
		for _, seg := range segments {
			if seg.Type != "reply" {
				hasTextSeg = true
				break
			}
		}
		if !hasTextSeg && plan.FallbackText != "" {
			segments = append(segments, conversationdomain.MessageSegment{
				Type: "text",
				Data: map[string]any{"text": plan.FallbackText},
			})
			hasTextSeg = true
		}
		if !hasTextSeg {
			slog.Warn("executor: no text content in reply, dropping send",
				"decision_id", decision.DecisionID,
				"group_id", event.GroupID,
				"reply_to", plan.ReplyToMessageID,
			)
			return replydomain.ActionExecution{}, errDropSend
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
