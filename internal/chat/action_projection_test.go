package chat

import (
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/qq"
)

// TestMarkBotActionDrivesContinuation covers the topic semantics of a
// confirmed platform action: bot_action_at participates in the active window
// and is reported separately from message participation.
func TestMarkBotActionDrivesContinuation(t *testing.T) {
	tracker := newTopicTracker()
	window := time.Minute
	first := qq.Event{ID: "e1", GroupID: 1, UserID: 10, Text: "你们看这个", At: time.Now()}
	obs := tracker.observe(1, nil, []qq.Event{first}, window)
	topicID := obs.Assignments["e1"][0]
	if obs.Continuing {
		t.Fatal("continuation without bot participation")
	}
	tracker.markBotAction(1, topicID, time.Now())
	obs = tracker.observe(1, []qq.Event{first}, []qq.Event{{ID: "e2", GroupID: 1, UserID: 11, Text: "确实好笑", At: time.Now().Add(time.Second)}}, window)
	if !obs.Continuing || !obs.ActiveBotTopics[topicID] {
		t.Fatal("action participation did not drive continuation")
	}
	found := false
	for _, topic := range obs.Topics {
		if topic.ID != topicID {
			continue
		}
		found = true
		if !topic.BotParticipated || !topic.BotActionParticipated {
			t.Fatal("participation flags wrong after action", topic)
		}
	}
	if !found {
		t.Fatal("topic missing from observation")
	}

	// Action participation stays separate from message participation: the
	// bot_message_at source is only written by markBotReply.
	replied := tracker.groups[1].topics[topicID]
	if !replied.botMessageAt.IsZero() {
		t.Fatal("action participation leaked into bot_message_at")
	}
}
