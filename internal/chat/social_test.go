package chat

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/qq"
)

func TestBuildConversationContextRelations(t *testing.T) {
	history := []qq.Event{
		{ID: "m1", MessageID: "1", UserID: 10, Text: "直接问你", Directed: true},
		{ID: "out", MessageID: "2", UserID: 99, Text: "我的回答", Outbound: true},
		{ID: "old", MessageID: "6", UserID: 12, Text: "旧闲聊"},
		{ID: "m5", MessageID: "5", UserID: 13, Text: "撤回", Recalled: true},
	}
	batch := []qq.Event{
		{ID: "m3", MessageID: "3", UserID: 11, Text: "回复机器人", ReplyTo: "2"},
		{ID: "m4", MessageID: "4", UserID: 11, Text: "接着说"},
		{ID: "m6", MessageID: "6", UserID: 12, Text: "另一个人闲聊"},
	}
	context := buildConversationContext(batch, history, topicObservation{
		Assignments: map[string][]string{
			"m1":  {"topic:1"},
			"out": {"topic:1"},
			"old": {"topic:2"},
			"m5":  {"topic:2"},
			"m3":  {"topic:1"},
			"m4":  {"topic:1"},
			"m6":  {"topic:2"},
		},
		ActiveBotTopics: map[string]bool{"topic:1": true},
	})

	want := map[string]string{"m3": "reply_to_bot", "m4": "continuation", "m6": "ordinary"}
	for _, event := range context.Batch {
		if event.Relation != want[event.EventID] {
			t.Fatalf("batch event %s relation = %q, want %q", event.EventID, event.Relation, want[event.EventID])
		}
	}
	want = map[string]string{"m1": "direct", "out": "bot_reply", "old": "ordinary", "m5": "recalled"}
	for _, event := range context.History {
		if event.Relation != want[event.EventID] {
			t.Fatalf("history event %s relation = %q, want %q", event.EventID, event.Relation, want[event.EventID])
		}
	}
}

func TestTopicGraphKeepsParallelAndQuotedThreads(t *testing.T) {
	tracker := newTopicTracker()
	now := time.Now()
	batch := []qq.Event{
		{Seq: 1, ID: "a1", MessageID: "1", UserID: 10, Text: "周末去爬山吗", At: now},
		{Seq: 2, ID: "b1", MessageID: "2", UserID: 20, Text: "今晚吃什么", At: now.Add(time.Second)},
		{Seq: 3, ID: "a2", MessageID: "3", UserID: 10, Text: "爬山也算我一个", ReplyTo: "1", At: now.Add(2 * time.Second)},
	}
	observation := tracker.observe(1, nil, batch, time.Minute)
	climbing := observation.Assignments["a1"]
	dinner := observation.Assignments["b1"]
	followup := observation.Assignments["a2"]
	if len(climbing) != 1 || len(dinner) != 1 || climbing[0] == dinner[0] {
		t.Fatalf("parallel topics were not separated: climbing=%v dinner=%v", climbing, dinner)
	}
	if len(followup) != 1 || followup[0] != climbing[0] {
		t.Fatalf("quoted followup = %v, want %v", followup, climbing)
	}

	tracker.markBotReply(1, climbing[0], qq.Event{ID: "out", MessageID: "4", UserID: 99, Outbound: true, At: now.Add(3 * time.Second)})
	reactivated := tracker.observe(1, nil, []qq.Event{{Seq: 4, ID: "b2", MessageID: "5", UserID: 11, Text: "我也想爬山", At: now.Add(4 * time.Second)}}, time.Minute)
	if ids := reactivated.Assignments["b2"]; len(ids) != 1 || ids[0] != climbing[0] {
		t.Fatalf("reactivated topic = %v, want %v", ids, climbing)
	}
	if !reactivated.ActiveBotTopics[climbing[0]] {
		t.Fatalf("bot topic was not active: %+v", reactivated.ActiveBotTopics)
	}
}

func TestTopicGraphAllowsSameAuthorAcrossTopics(t *testing.T) {
	tracker := newTopicTracker()
	now := time.Now()
	batch := []qq.Event{
		{Seq: 1, ID: "a1", MessageID: "1", UserID: 10, Text: "周末爬香山", At: now},
		{Seq: 2, ID: "b1", MessageID: "2", UserID: 20, Text: "今晚吃火锅", At: now.Add(time.Second)},
		{Seq: 3, ID: "a2", MessageID: "3", UserID: 10, Text: "香山几点集合", At: now.Add(2 * time.Second)},
	}
	observation := tracker.observe(1, nil, batch, time.Minute)
	mountain, dinner, followup := observation.Assignments["a1"], observation.Assignments["b1"], observation.Assignments["a2"]
	if len(mountain) != 1 || len(dinner) != 1 || mountain[0] == dinner[0] {
		t.Fatalf("parallel topics were not separated: mountain=%v dinner=%v", mountain, dinner)
	}
	if len(followup) != 1 || followup[0] != mountain[0] {
		t.Fatalf("same author stayed in the wrong topic: followup=%v mountain=%v", followup, mountain)
	}
}

func TestTopicGraphKeepsCloseLexicalAmbiguity(t *testing.T) {
	tracker := newTopicTracker()
	now := time.Now()
	batch := []qq.Event{
		{Seq: 1, ID: "a1", MessageID: "1", UserID: 10, Text: "周末去爬山", At: now},
		{Seq: 2, ID: "b1", MessageID: "2", UserID: 20, Text: "今晚吃火锅", At: now.Add(time.Second)},
		{Seq: 3, ID: "c1", MessageID: "3", UserID: 30, Text: "爬山还是火锅", At: now.Add(2 * time.Second)},
	}
	observation := tracker.observe(1, nil, batch, time.Minute)
	if ids := observation.Assignments["c1"]; len(ids) != 2 {
		t.Fatalf("ambiguous message topic ids = %v, want two candidates", ids)
	}
}

func TestTopicGraphDoesNotDuplicateReplayedText(t *testing.T) {
	tracker := newTopicTracker()
	now := time.Now()
	batch := []qq.Event{{Seq: 1, ID: "a1", MessageID: "1", UserID: 10, Text: "周末去爬山", At: now}}
	observation := tracker.observe(1, nil, batch, time.Minute)
	topicID := observation.Assignments["a1"][0]
	tracker.observe(1, batch, nil, time.Minute)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if texts := tracker.groups[1].topics[topicID].texts; len(texts) != 1 {
		t.Fatalf("replayed event produced duplicate topic text: %v", texts)
	}
}

func TestTopicGraphBoundsAndRemovesTextIndex(t *testing.T) {
	tracker := newTopicTracker()
	now := time.Now()
	events := make([]qq.Event, 0, maxTopicEvents+3)
	previousMessageID := ""
	for i := 0; i < maxTopicEvents+3; i++ {
		event := qq.Event{
			Seq:       int64(i + 1),
			ID:        fmt.Sprintf("m%d", i),
			MessageID: fmt.Sprintf("%d", i+1),
			UserID:    10,
			Text:      fmt.Sprintf("连续消息%d", i),
			ReplyTo:   previousMessageID,
			At:        now.Add(time.Duration(i) * time.Second),
		}
		previousMessageID = event.MessageID
		events = append(events, event)
	}

	observation := tracker.observe(1, nil, events, time.Minute)
	topicID := observation.Assignments[events[len(events)-1].ID][0]
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	graph := tracker.groups[1]
	topic := graph.topics[topicID]
	if topic == nil {
		t.Fatal("topic was not created")
	}
	if len(topic.texts) != maxTopicEvents || len(topic.textEventOrder) != maxTopicEvents || len(topic.textEventIDs) != maxTopicEvents {
		t.Fatalf("text index was not bounded: texts=%d order=%d ids=%d", len(topic.texts), len(topic.textEventOrder), len(topic.textEventIDs))
	}
	for i, eventID := range topic.textEventOrder {
		if !topic.textEventIDs[eventID] {
			t.Fatalf("text index order contains untracked event %q at %d", eventID, i)
		}
	}

	removedID := topic.textEventOrder[0]
	graph.removeEventAssignment(removedID)
	if containsString(topic.eventIDs, removedID) || topic.textEventIDs[removedID] {
		t.Fatalf("removed event %q remained in topic indexes", removedID)
	}
	if len(topic.texts) != len(topic.textEventOrder) || len(topic.textEventOrder) != len(topic.textEventIDs) {
		t.Fatalf("text index drifted after removal: texts=%d order=%d ids=%d", len(topic.texts), len(topic.textEventOrder), len(topic.textEventIDs))
	}
}

func TestTopicGraphBoundsParticipants(t *testing.T) {
	tracker := newTopicTracker()
	now := time.Now()
	root := qq.Event{Seq: 1, ID: "root", MessageID: "1", UserID: 1, Text: "开始一个话题", At: now}
	events := []qq.Event{root}
	for i := 0; i < maxTopicParticipants+3; i++ {
		events = append(events, qq.Event{
			Seq:       int64(i + 2),
			ID:        fmt.Sprintf("reply%d", i),
			MessageID: fmt.Sprintf("%d", i+2),
			UserID:    int64(i + 2),
			Text:      fmt.Sprintf("我也来说一句%d", i),
			ReplyTo:   root.MessageID,
			At:        now.Add(time.Duration(i+1) * time.Second),
		})
	}

	observation := tracker.observe(1, nil, events, time.Minute)
	topicID := observation.Assignments[root.ID][0]
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	topic := tracker.groups[1].topics[topicID]
	if len(topic.participants) != maxTopicParticipants || len(topic.participantOrder) != maxTopicParticipants {
		t.Fatalf("participants were not bounded: map=%d order=%d", len(topic.participants), len(topic.participantOrder))
	}
	if topic.participants[1] || !topic.participants[int64(maxTopicParticipants+4)] {
		t.Fatalf("participant retention did not keep the most recent users: order=%v", topic.participantOrder)
	}
}

func TestTopicTrackerBoundsGroupGraphs(t *testing.T) {
	tracker := newTopicTracker()
	for groupID := int64(1); groupID <= maxTopicGroups; groupID++ {
		tracker.observe(groupID, nil, nil, time.Minute)
	}
	tracker.mu.Lock()
	for groupID, graph := range tracker.groups {
		graph.lastActive = time.Unix(groupID, 0)
	}
	tracker.mu.Unlock()

	tracker.observe(maxTopicGroups+1, nil, nil, time.Minute)
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if len(tracker.groups) != maxTopicGroups {
		t.Fatalf("group graph count = %d, want %d", len(tracker.groups), maxTopicGroups)
	}
	if tracker.groups[1] != nil || tracker.groups[maxTopicGroups+1] == nil {
		t.Fatalf("oldest group was not evicted: oldest=%v newest=%v", tracker.groups[1], tracker.groups[maxTopicGroups+1])
	}
}

func TestTopicGraphSharesRecalledMessageAssignment(t *testing.T) {
	tracker := newTopicTracker()
	now := time.Now()
	original := qq.Event{Seq: 1, ID: "original", MessageID: "1", UserID: 10, Text: "周末去爬山", At: now}
	recall := qq.Event{Seq: 2, ID: "recall", MessageID: "1", UserID: 10, Recalled: true, At: now.Add(time.Second)}
	tracker.observe(1, nil, []qq.Event{original}, time.Minute)
	tracker.observe(1, nil, []qq.Event{recall}, time.Minute)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	graph := tracker.groups[1]
	if graph.messageEvents[original.MessageID] != 2 {
		t.Fatalf("message event references = %d, want 2", graph.messageEvents[original.MessageID])
	}
	graph.removeEventAssignment(original.ID)
	if graph.messageEvents[original.MessageID] != 1 || len(graph.messageTopics[original.MessageID]) == 0 {
		t.Fatalf("recalled event did not retain message assignment: events=%d topics=%v", graph.messageEvents[original.MessageID], graph.messageTopics[original.MessageID])
	}
	graph.removeEventAssignment(recall.ID)
	if _, exists := graph.messageEvents[original.MessageID]; exists {
		t.Fatalf("message reference was not released: %v", graph.messageEvents)
	}
	if _, exists := graph.messageTopics[original.MessageID]; exists {
		t.Fatalf("message topic mapping was not released: %v", graph.messageTopics)
	}
}

func TestTopicGraphKeepsRecallInOriginalTopic(t *testing.T) {
	tracker := newTopicTracker()
	now := time.Now()
	initial := tracker.observe(1, nil, []qq.Event{{Seq: 1, ID: "a1", MessageID: "1", UserID: 10, Text: "周末去爬山", At: now}}, time.Minute)
	recall := tracker.observe(1, nil, []qq.Event{{Seq: 2, ID: "r1", MessageID: "1", UserID: 10, Recalled: true, At: now.Add(time.Second)}}, time.Minute)
	if got, want := recall.Assignments["r1"], initial.Assignments["a1"]; len(got) != 1 || len(want) != 1 || got[0] != want[0] {
		t.Fatalf("recall topic = %v, want original topic %v", got, want)
	}
}

func TestValidateReplyPlanChecksTopicRelationship(t *testing.T) {
	history := []qq.Event{
		{ID: "m1", MessageID: "1", UserID: 10, Text: "原消息"},
		{ID: "m2", MessageID: "2", UserID: 11, Text: "另一条消息"},
	}
	topics := topicObservation{Assignments: map[string][]string{
		"m1": {"topic:1:1"},
		"m2": {"topic:1:2"},
	}}

	reply := Reply{Action: "text", Text: "收到", Plan: &ReplyPlan{TargetEventID: "m1", ResponseMode: "short"}}
	if err := validateReplyPlan(reply, history, topics); err != nil || reply.Plan.TopicID != "topic:1:1" {
		t.Fatalf("target topic was not inferred: plan=%+v err=%v", reply.Plan, err)
	}

	invisible := Reply{Action: "text", Text: "收到", Plan: &ReplyPlan{TopicID: "topic:1:99", ResponseMode: "short"}}
	if err := validateReplyPlan(invisible, history, topics); !errors.Is(err, errInvisibleTopic) {
		t.Fatalf("invisible topic error = %v", err)
	}
	mismatch := Reply{Action: "text", Text: "收到", Plan: &ReplyPlan{TargetEventID: "m1", TopicID: "topic:1:2", ResponseMode: "short"}}
	if err := validateReplyPlan(mismatch, history, topics); !errors.Is(err, errTopicTargetMismatch) {
		t.Fatalf("topic/target mismatch error = %v", err)
	}
}

func TestReplyParticipantsFollowsTarget(t *testing.T) {
	events := []qq.Event{
		{ID: "direct", UserID: 10, Directed: true},
		{ID: "ordinary", UserID: 11},
	}
	participants := replyParticipants(events, &ReplyPlan{TargetEventID: "ordinary"})
	if len(participants) != 1 || !participants[11] {
		t.Fatalf("participants = %v, want only target user", participants)
	}

	participants = replyParticipants(events, &ReplyPlan{})
	if len(participants) != 1 || !participants[10] {
		t.Fatalf("fallback participants = %v, want direct user", participants)
	}
}
