package chat

import (
	"errors"

	"github.com/phlin/go-agent/internal/qq"
)

var (
	errInvalidResponseMode  = errors.New("invalid response mode")
	errInvisibleReplyTarget = errors.New("reply target is not visible")
)

// ReplyPlan records the model's participation target and expression shape.
// The runtime validates the target before any message is sent.
type ReplyPlan struct {
	TargetEventID string `json:"target_event_id,omitempty"`
	ResponseMode  string `json:"response_mode,omitempty"`
}

// ConversationEvent is a compact relation hint for an event already present
// in the history or current batch. The event text remains in the normal
// history and batch inputs.
type ConversationEvent struct {
	EventID   string `json:"event_id"`
	MessageID string `json:"message_id,omitempty"`
	UserID    int64  `json:"user_id"`
	Relation  string `json:"relation"`
}

// ConversationContext gives the model explicit participation cues without
// introducing a second model call or a persistent topic graph.
type ConversationContext struct {
	Continuing bool                `json:"continuing"`
	Batch      []ConversationEvent `json:"batch"`
	History    []ConversationEvent `json:"history"`
}

func buildConversationContext(batch, history []qq.Event, continuingUsers map[int64]bool) ConversationContext {
	history = priorConversation(history, batch)
	visible := combineEvents(history, batch)
	context := ConversationContext{Continuing: len(continuingUsers) > 0}
	context.Batch = make([]ConversationEvent, 0, len(batch))
	for _, e := range batch {
		context.Batch = append(context.Batch, describeConversationEvent(e, visible, continuingUsers[e.UserID]))
	}
	context.History = make([]ConversationEvent, 0, len(history))
	for _, e := range history {
		context.History = append(context.History, describeConversationEvent(e, visible, false))
	}
	return context
}

func priorConversation(history, batch []qq.Event) []qq.Event {
	current := make(map[string]bool, len(batch))
	for _, e := range batch {
		if e.ID != "" {
			current[e.ID] = true
		}
	}
	out := make([]qq.Event, 0, len(history))
	for _, e := range history {
		if e.ID == "" || !current[e.ID] {
			out = append(out, e)
		}
	}
	return out
}

func combineEvents(history, batch []qq.Event) []qq.Event {
	out := make([]qq.Event, 0, len(history)+len(batch))
	seen := make(map[string]bool, len(history)+len(batch))
	for _, events := range [][]qq.Event{history, batch} {
		for _, e := range events {
			if e.ID != "" && seen[e.ID] {
				continue
			}
			if e.ID != "" {
				seen[e.ID] = true
			}
			out = append(out, e)
		}
	}
	return out
}

func describeConversationEvent(e qq.Event, history []qq.Event, continuing bool) ConversationEvent {
	relation := "ordinary"
	switch {
	case e.Recalled:
		relation = "recalled"
	case e.Outbound:
		relation = "bot_reply"
	case repliesToBot(e, history):
		relation = "reply_to_bot"
	case e.Directed:
		relation = "direct"
	case continuing:
		relation = "continuation"
	}
	return ConversationEvent{EventID: e.ID, MessageID: e.MessageID, UserID: e.UserID, Relation: relation}
}

func repliesToBot(e qq.Event, history []qq.Event) bool {
	if e.ReplyTo == "" {
		return false
	}
	for _, candidate := range history {
		if candidate.Outbound && !candidate.Recalled && candidate.MessageID == e.ReplyTo {
			return true
		}
	}
	return false
}

func replyParticipants(events []qq.Event, plan *ReplyPlan) map[int64]bool {
	participants := map[int64]bool{}
	if plan != nil && plan.TargetEventID != "" {
		for _, e := range events {
			if e.ID == plan.TargetEventID && !e.Recalled {
				participants[e.UserID] = true
				return participants
			}
		}
	}
	for _, e := range events {
		if e.Directed && !e.Recalled {
			participants[e.UserID] = true
		}
	}
	if len(participants) == 0 && len(events) > 0 {
		participants[events[len(events)-1].UserID] = true
	}
	return participants
}

func normalizeReply(reply *Reply) {
	if reply.Plan == nil {
		reply.Plan = &ReplyPlan{}
	}
	if reply.Plan.ResponseMode == "" {
		reply.Plan.ResponseMode = defaultResponseMode(reply.Action)
	}
}

func defaultResponseMode(action string) string {
	switch action {
	case "silent":
		return "silent"
	case "sticker":
		return "sticker"
	default:
		return "short"
	}
}

func validateReplyPlan(reply Reply, history []qq.Event) error {
	if reply.Plan == nil {
		return nil
	}
	validModes := map[string]bool{"silent": true, "ack": true, "short": true, "answer": true, "sticker": true}
	if !validModes[reply.Plan.ResponseMode] {
		return errInvalidResponseMode
	}
	if reply.Plan.TargetEventID != "" {
		found := false
		for _, e := range history {
			if e.ID == reply.Plan.TargetEventID && !e.Recalled {
				found = true
				break
			}
		}
		if !found {
			return errInvisibleReplyTarget
		}
	}
	switch reply.Action {
	case "silent":
		if reply.Plan.ResponseMode != "silent" {
			return errInvalidResponseMode
		}
	case "text":
		if reply.Plan.ResponseMode != "ack" && reply.Plan.ResponseMode != "short" && reply.Plan.ResponseMode != "answer" {
			return errInvalidResponseMode
		}
	case "sticker":
		if reply.Plan.ResponseMode != "sticker" {
			return errInvalidResponseMode
		}
	}
	return nil
}
