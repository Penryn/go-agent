package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	backgroundruntime "github.com/phlin/go-agent/internal/runtime/background"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
	outputguardsvc "github.com/phlin/go-agent/internal/services/outputguard"
)

var errDropSend = errors.New("drop send: no text content")
var errGuardSilenced = errors.New("drop send: guard silenced")

type Service struct {
	sender     ports.OutboundSender
	memes      *memesvc.Service
	guard      *outputguardsvc.Guard // 可为 nil，nil 时跳过清洗
	background backgroundSubmitter
	outbox     interface {
		Enqueue(context.Context, string, string, []byte) error
	}
	presence  PresenceObserver
	selfID    int64
	rhythmMu  sync.Mutex
	rhythm    map[int64]rhythmEntry
	rhythmSeq uint64
}

type rhythmEntry struct {
	token  uint64
	cancel context.CancelFunc
}

type PresenceObserver interface {
	Observe(context.Context, humandomain.EventRecord) (humandomain.GroupWorkingMemory, error)
}

type backgroundSubmitter interface {
	Submit(backgroundruntime.Job) bool
}

// WithBackgroundRuntime routes post-send work through the application job
// owner instead of creating an untracked goroutine.
func WithBackgroundRuntime(runtime backgroundSubmitter) Option {
	return func(s *Service) { s.background = runtime }
}

func WithOutbox(runtime interface {
	Enqueue(context.Context, string, string, []byte) error
}) Option {
	return func(s *Service) { s.outbox = runtime }
}

type MarkMemeSentTask struct {
	MemeID string `json:"meme_id"`
}

func WithPresenceObserver(observer PresenceObserver) Option {
	return func(s *Service) { s.presence = observer }
}

func WithSelfID(selfID int64) Option {
	return func(s *Service) { s.selfID = selfID }
}

type Option func(*Service)

// bubbleDelay 是分条气泡之间的发送间隔。测试通过覆盖该变量调整节奏。
var bubbleDelay = 350 * time.Millisecond

func New(sender ports.OutboundSender, memes *memesvc.Service, guard *outputguardsvc.Guard, opts ...Option) *Service {
	service := &Service{sender: sender, memes: memes, guard: guard, rhythm: make(map[int64]rhythmEntry)}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// CancelQueued drops unsent bubbles for a group when a newer message arrives.
func (s *Service) CancelQueued(groupID int64) {
	s.rhythmMu.Lock()
	if entry, ok := s.rhythm[groupID]; ok {
		entry.cancel()
		delete(s.rhythm, groupID)
	}
	s.rhythmMu.Unlock()
}

// markRead 在发送前调用平台的已读回执（若 sender 支持）。
func (s *Service) markRead(ctx context.Context, event conversationdomain.ConversationEvent) {
	acker, ok := s.sender.(ports.ReadAckingSender)
	if !ok {
		return
	}
	if err := acker.MarkRead(ctx, event.GroupID, event.MessageID); err != nil {
		slog.Debug("executor: mark read failed", "group_id", event.GroupID, "err", err)
	}
}

func (s *Service) Execute(ctx context.Context, event conversationdomain.ConversationEvent, decision policydomain.AutonomyDecision, plan replydomain.ReplyPlan) (replydomain.ActionReceipt, error) {
	// 已读回执：确定要发言后先把该群标记已读，模拟「看到→才回」。
	// 失败静默——回执是拟人增强，不构成发送前置条件。
	if decision.Action != policydomain.ActionSilent {
		s.markRead(ctx, event)
	}
	if decision.Action == policydomain.ActionReply && len(plan.Bubbles) > 1 {
		return s.executeRhythm(ctx, event, decision, plan)
	}
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
	if receipt.Sent && s.presence != nil {
		selfEvent := conversationdomain.ConversationEvent{
			EventID:          "outbound-" + action.ActionID,
			GroupID:          action.GroupID,
			UserID:           s.selfID,
			MessageID:        receipt.PlatformMessageID,
			ReplyToMessageID: action.ReplyToMessageID,
			Kind:             conversationdomain.EventMessage,
			Segments:         action.Segments,
			Text:             actionText(action.Segments),
			TimestampUnix:    time.Now().Unix(),
		}
		if _, observeErr := s.presence.Observe(ctx, humandomain.EventRecord{
			EventID:   selfEvent.EventID,
			GroupID:   selfEvent.GroupID,
			UserID:    selfEvent.UserID,
			Origin:    humandomain.OriginOutbound,
			Timestamp: time.Now(),
			Event:     selfEvent,
		}); observeErr != nil {
			slog.Warn("executor: observe outbound event failed", "action_id", action.ActionID, "err", observeErr)
		}
	}

	// B-1: 发送成功后提交标记表情包已发送的后台任务。
	if receipt.Sent && decision.Action == policydomain.ActionMemeOnly {
		if memeID, _ := plan.ActionParams["meme_id"].(string); memeID != "" && s.memes != nil {
			bgCtx := context.WithoutCancel(ctx)
			if s.outbox != nil {
				payload, marshalErr := json.Marshal(MarkMemeSentTask{MemeID: memeID})
				if marshalErr == nil {
					if enqueueErr := s.outbox.Enqueue(bgCtx, "meme_mark_sent", memeID, payload); enqueueErr == nil {
						return receipt, nil
					} else {
						marshalErr = enqueueErr
					}
				}
				slog.Warn("executor: outbox enqueue failed, using process-local queue", "meme_id", memeID, "err", marshalErr)
			}
			memes := s.memes
			job := backgroundruntime.Job{
				Name:    "meme_mark_sent",
				Timeout: 10 * time.Second,
				Run: func(jobCtx context.Context) error {
					return memes.MarkSent(jobCtx, memeID)
				},
			}
			if s.background != nil {
				if !s.background.Submit(job) {
					slog.Warn("executor: MarkSent job dropped", "meme_id", memeID)
				}
			} else {
				backgroundruntime.RunInline(bgCtx, job)
			}
		}
	}

	return receipt, nil
}

func (s *Service) executeRhythm(ctx context.Context, event conversationdomain.ConversationEvent, decision policydomain.AutonomyDecision, plan replydomain.ReplyPlan) (replydomain.ActionReceipt, error) {
	rhythmCtx, cancel := context.WithCancel(ctx)
	s.rhythmMu.Lock()
	if previous, ok := s.rhythm[event.GroupID]; ok {
		previous.cancel()
	}
	s.rhythmSeq++
	token := s.rhythmSeq
	s.rhythm[event.GroupID] = rhythmEntry{token: token, cancel: cancel}
	s.rhythmMu.Unlock()
	defer func() {
		s.rhythmMu.Lock()
		if current, ok := s.rhythm[event.GroupID]; ok && current.token == token {
			delete(s.rhythm, event.GroupID)
		}
		s.rhythmMu.Unlock()
		cancel()
	}()

	var receipt replydomain.ActionReceipt
	for i, bubble := range plan.Bubbles {
		if i > 0 {
			timer := time.NewTimer(bubbleDelay)
			select {
			case <-rhythmCtx.Done():
				timer.Stop()
				return receipt, nil
			case <-timer.C:
			}
		}
		part := plan
		part.Bubbles = []string{bubble}
		if i > 0 {
			part.ReplyToMessageID = ""
		}
		var err error
		receipt, err = s.Execute(rhythmCtx, event, decision, part)
		if err != nil {
			return receipt, err
		}
	}
	return receipt, nil
}

func actionText(segments []conversationdomain.MessageSegment) string {
	var text string
	for _, segment := range segments {
		if segment.Type != "text" {
			continue
		}
		if value, ok := segment.Data["text"].(string); ok {
			text += value
		}
	}
	return text
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
