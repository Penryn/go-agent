package chat

import (
	"errors"

	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/qq"
)

var (
	errInvalidResponseMode  = errors.New("invalid response mode")
	errInvisibleReplyTarget = errors.New("reply target is not visible")
	errInvisibleTopic       = errors.New("topic is not visible")
	errTopicTargetMismatch  = errors.New("target event is not part of the topic")
)

// ReactionEmojis is the fixed reaction candidate set offered to the model as
// reaction_emoji_candidates; validation rejects anything outside it.
// ponytail: QQ face indexes are not documented in the OneBot/NapCat OpenAPI
// (the spec only shows "123" as an example); confirm these on the target
// NapCat instance during manual acceptance and edit this single map.
var ReactionEmojis = map[string]string{
	"76": "赞",
	"66": "爱心",
}

// pokeTarget resolves the poke target from the plan's target event author.
// Routing pokes through a visible event structurally guarantees the target
// belongs to the current group conversation.
func pokeTarget(reply Reply, history, batch []qq.Event) (int64, error) {
	if reply.Plan == nil || reply.Plan.TargetEventID == "" {
		return 0, errors.New("poke requires a target event")
	}
	for _, e := range combineEvents(history, batch) {
		if e.ID == reply.Plan.TargetEventID {
			if e.Outbound || e.UserID == 0 {
				return 0, errors.New("poke target must be a group member")
			}
			return e.UserID, nil
		}
	}
	return 0, errInvisibleReplyTarget
}

// ReplyPlan records the model's participation target and expression shape.
// The runtime validates the target before any message is sent.
type ReplyPlan struct {
	TargetEventID string `json:"target_event_id,omitempty"`
	TopicID       string `json:"topic_id,omitempty"`
	ResponseMode  string `json:"response_mode,omitempty"`
}

// Relation types live in conversation so package context can build snapshots
// without importing chat.
type (
	ConversationEvent   = conversation.Event
	ConversationTopic   = conversation.Topic
	ConversationContext = conversation.Context
)

func buildConversationContext(batch, history []qq.Event, topics topicObservation) ConversationContext {
	history = priorConversation(history, batch)
	visible := combineEvents(history, batch)
	return buildConversationContextFromVisible(batch, history, visible, topics)
}

func buildConversationContextFromVisible(batch, history, visible []qq.Event, topics topicObservation) ConversationContext {
	context := ConversationContext{Topics: topics.Topics}
	context.Batch = make([]ConversationEvent, 0, len(batch))
	for _, e := range batch {
		topicIDs := topics.Assignments[e.ID]
		continuing := topics.continuesTopic(topicIDs)
		context.Continuing = context.Continuing || continuing
		context.Batch = append(context.Batch, describeConversationEvent(e, visible, topicIDs, continuing))
	}
	context.History = make([]ConversationEvent, 0, len(history))
	for _, e := range history {
		topicIDs := topics.Assignments[e.ID]
		context.History = append(context.History, describeConversationEvent(e, visible, topicIDs, topics.continuesTopic(topicIDs)))
	}
	return context
}

func (o topicObservation) continuesTopic(topicIDs []string) bool {
	for _, topicID := range topicIDs {
		if o.ActiveBotTopics[topicID] {
			return true
		}
	}
	return false
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

func describeConversationEvent(e qq.Event, history []qq.Event, topicIDs []string, continuing bool) ConversationEvent {
	relation := "ordinary"
	switch {
	case e.Recalled:
		relation = "recalled"
	case e.Outbound:
		relation = "bot_reply"
	case e.Kind == "poke":
		if e.TargetUserID == e.SelfID {
			relation = "poke_to_bot"
		} else {
			relation = "poke"
		}
	case e.Kind == "reaction" && reactionToBot(e, history):
		relation = "reaction_to_bot"
	case e.Kind == "reaction":
		relation = "reaction"
	case repliesToBot(e, history):
		relation = "reply_to_bot"
	case e.Directed:
		relation = "direct"
	case continuing:
		relation = "continuation"
	}
	return ConversationEvent{EventID: e.ID, MessageID: e.MessageID, UserID: e.UserID, Relation: relation, TopicIDs: append([]string(nil), topicIDs...)}
}

// reactionToBot reports whether an inbound reaction targets one of the bot's
// archived messages. Directed is set from it during admission so a reaction
// to the bot's message counts as being addressed.
func reactionToBot(e qq.Event, history []qq.Event) bool {
	if e.Kind != "reaction" || e.TargetMessageID == "" {
		return false
	}
	for _, candidate := range history {
		if candidate.Outbound && !candidate.Recalled && candidate.MessageID == e.TargetMessageID {
			return true
		}
	}
	return false
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

func validateReplyPlan(reply Reply, history []qq.Event, topics topicObservation) error {
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
		if reply.Plan.TopicID == "" {
			if ids := topics.Assignments[reply.Plan.TargetEventID]; len(ids) > 0 {
				reply.Plan.TopicID = ids[0]
			}
		}
	}
	if reply.Plan.TopicID != "" {
		visible := false
		for _, ids := range topics.Assignments {
			if containsString(ids, reply.Plan.TopicID) {
				visible = true
				break
			}
		}
		if !visible {
			return errInvisibleTopic
		}
		if reply.Plan.TargetEventID != "" && !containsString(topics.Assignments[reply.Plan.TargetEventID], reply.Plan.TopicID) {
			return errTopicTargetMismatch
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
