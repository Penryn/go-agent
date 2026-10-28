package chat

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/search/bm25"
)

const (
	maxTopicCandidates   = 2
	maxTopicGroups       = 256
	maxTopicsPerGroup    = 64
	maxTopicEvents       = 12
	maxTopicParticipants = 32
	maxTopicAssignments  = 512
	maxContextTopics     = 12
	maxTopicContextEvent = 6
	topicLexicalFloor    = 0.35
	topicLexicalRelative = 0.72
)

type topicTracker struct {
	mu     sync.Mutex
	groups map[int64]*topicGraph
}

type topicGraph struct {
	groupID         int64
	nextID          int
	lastActive      time.Time
	topics          map[string]*topicState
	eventTopics     map[string][]string
	eventMessageIDs map[string]string
	messageEvents   map[string]int
	messageTopics   map[string][]string
	eventOrder      []string
	lastLiveEvent   *qq.Event
}

type topicState struct {
	id               string
	participants     map[int64]bool
	participantOrder []int64
	eventIDs         []string
	textEventIDs     map[string]bool
	textEventOrder   []string
	texts            []string
	lastSeq          int64
	lastAt           time.Time
	firstSeq         int64
	summary          string
	revision         int64
	botParticipated  bool
	botAt            time.Time
}

type topicObservation struct {
	Assignments     map[string][]string
	Topics          []ConversationTopic
	ActiveBotTopics map[string]bool
	Continuing      bool
}

type topicCandidate struct {
	id    string
	score float64
}

func newTopicTracker() *topicTracker {
	return &topicTracker{groups: map[int64]*topicGraph{}}
}

func (t *topicTracker) Snapshot(groupID int64) []conversation.TopicView {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.groups[groupID]
	if g == nil {
		return nil
	}
	out := make([]conversation.TopicView, 0, len(g.topics))
	for _, topic := range g.topics {
		out = append(out, conversation.TopicView{ID: topic.id, Summary: topic.summary, Participants: sortedTopicParticipants(topic), FirstSeq: topic.firstSeq, LastSeq: topic.lastSeq, Revision: topic.revision, LastActiveAt: topic.lastAt, Status: topicStateAt(topic, time.Now(), 90*time.Second)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActiveAt.After(out[j].LastActiveAt) })
	return out
}

func (t *topicTracker) Restore(groupID int64, views []conversation.TopicView) {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.groups[groupID]
	if g == nil {
		g = &topicGraph{groupID: groupID, topics: map[string]*topicState{}, eventTopics: map[string][]string{}, eventMessageIDs: map[string]string{}, messageEvents: map[string]int{}, messageTopics: map[string][]string{}}
		t.groups[groupID] = g
	}
	for _, view := range views {
		if view.ID == "" {
			continue
		}
		topic := &topicState{id: view.ID, participants: map[int64]bool{}, textEventIDs: map[string]bool{}, firstSeq: view.FirstSeq, lastSeq: view.LastSeq, lastAt: view.LastActiveAt, summary: view.Summary, revision: view.Revision}
		for _, id := range view.Participants {
			topic.addParticipant(id)
		}
		if topic.revision < 1 {
			topic.revision = 1
		}
		g.topics[topic.id] = topic
	}
	g.lastActive = time.Now()
}

// observe incrementally updates a bounded group topic graph. The graph is
// deliberately deterministic: explicit replies and lexical matches shape
// membership, while participants and recency only re-rank existing lexical
// candidates. It does not invent a persistent history across process restarts.
func (t *topicTracker) observe(groupID int64, history, batch []qq.Event, activeWindow time.Duration) topicObservation {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	graph := t.groups[groupID]
	if graph == nil {
		if len(t.groups) >= maxTopicGroups {
			t.evictOldestGroup()
		}
		graph = &topicGraph{
			groupID:         groupID,
			topics:          map[string]*topicState{},
			eventTopics:     map[string][]string{},
			eventMessageIDs: map[string]string{},
			messageEvents:   map[string]int{},
			messageTopics:   map[string][]string{},
		}
		t.groups[groupID] = graph
	}
	graph.lastActive = now

	visibleOrder := make([]string, 0, len(history)+len(batch))
	seen := make(map[string]bool, len(history)+len(batch))
	for _, events := range [][]qq.Event{history, batch} {
		for _, event := range events {
			if event.ID == "" || seen[event.ID] {
				continue
			}
			seen[event.ID] = true
			visibleOrder = append(visibleOrder, event.ID)
			graph.assign(event)
			if !event.Recalled {
				copy := event
				graph.lastLiveEvent = &copy
			}
		}
	}

	observation := topicObservation{
		Assignments:     make(map[string][]string, len(visibleOrder)),
		ActiveBotTopics: map[string]bool{},
	}
	visibleTopics := map[string]bool{}
	for _, eventID := range visibleOrder {
		ids := append([]string(nil), graph.eventTopics[eventID]...)
		observation.Assignments[eventID] = ids
		for _, topicID := range ids {
			visibleTopics[topicID] = true
		}
	}
	for topicID := range visibleTopics {
		topic := graph.topics[topicID]
		if topic == nil {
			continue
		}
		if topic.botParticipated && activeWindow > 0 && now.Sub(topic.botAt) <= activeWindow {
			observation.ActiveBotTopics[topicID] = true
		}
		eventIDs := graph.visibleTopicEvents(topic, visibleTopics, visibleOrder)
		observation.Topics = append(observation.Topics, ConversationTopic{
			ID:              topic.id,
			State:           topicStateAt(topic, now, activeWindow),
			Participants:    sortedTopicParticipants(topic),
			EventIDs:        eventIDs,
			BotParticipated: topic.botParticipated,
		})
	}
	sort.Slice(observation.Topics, func(i, j int) bool {
		left, right := graph.topics[observation.Topics[i].ID], graph.topics[observation.Topics[j].ID]
		if left.lastSeq != right.lastSeq {
			return left.lastSeq > right.lastSeq
		}
		return observation.Topics[i].ID < observation.Topics[j].ID
	})
	if len(observation.Topics) > maxContextTopics {
		observation.Topics = observation.Topics[:maxContextTopics]
	}
	for _, eventID := range visibleOrder {
		for _, topicID := range observation.Assignments[eventID] {
			if observation.ActiveBotTopics[topicID] {
				observation.Continuing = true
			}
		}
	}
	return observation
}

func (t *topicTracker) markBotReply(groupID int64, topicID string, event qq.Event) {
	if topicID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	graph := t.groups[groupID]
	if graph == nil {
		return
	}
	topic := graph.topics[topicID]
	if topic == nil {
		return
	}
	graph.lastActive = time.Now()
	topic.botParticipated = true
	topic.botAt = event.At
	if topic.botAt.IsZero() {
		topic.botAt = time.Now()
	}
	graph.record(event.ID, []string{topicID}, event.MessageID)
	graph.touch(topic, event)
}

func (t *topicTracker) topicParticipants(groupID int64, topicID string) map[int64]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[int64]bool{}
	graph := t.groups[groupID]
	if graph == nil || graph.topics[topicID] == nil {
		return out
	}
	for userID := range graph.topics[topicID].participants {
		out[userID] = true
	}
	return out
}

func (g *topicGraph) assign(event qq.Event) []string {
	if ids := g.eventTopics[event.ID]; len(ids) > 0 {
		for _, id := range ids {
			if topic := g.topics[id]; topic != nil {
				g.touch(topic, event)
			}
		}
		return ids
	}

	var ids []string
	if event.Recalled {
		if event.MessageID != "" {
			ids = append(ids, g.messageTopics[event.MessageID]...)
		}
	} else if event.ReplyTo != "" {
		ids = g.messageTopics[event.ReplyTo]
	}
	if len(ids) == 0 && event.Outbound && event.ReplyTo == "" {
		ids = g.latestBotTopic()
	}
	if len(ids) == 0 && !event.Recalled {
		ids = g.lexicalTopics(event)
	}
	if len(ids) == 0 && !event.Recalled && g.isSameLiveTurn(event) && g.lastLiveEvent != nil {
		ids = g.eventTopics[g.lastLiveEvent.ID]
	}
	ids = uniqueTopicIDs(ids)
	if len(ids) > maxTopicCandidates {
		ids = ids[:maxTopicCandidates]
	}
	if len(ids) == 0 {
		if event.Recalled {
			return nil
		}
		topic := g.newTopic()
		ids = []string{topic.id}
	}
	for _, id := range ids {
		if topic := g.topics[id]; topic != nil {
			g.touch(topic, event)
		}
	}
	g.record(event.ID, ids, event.MessageID)
	return append([]string(nil), ids...)
}

func (g *topicGraph) lexicalTopics(event qq.Event) []string {
	if strings.TrimSpace(event.Text) == "" || len(g.topics) == 0 {
		return nil
	}
	documents := make([]bm25.Document, 0, len(g.topics))
	for id, topic := range g.topics {
		text := strings.TrimSpace(strings.Join(topic.texts, "\n"))
		if text != "" {
			documents = append(documents, bm25.Document{ID: id, Text: text})
		}
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].ID < documents[j].ID })
	ranked := bm25.Rank(event.Text, documents, maxTopicCandidates*2)
	if len(ranked) == 0 || ranked[0].Score < topicLexicalFloor {
		return nil
	}
	top := ranked[0].Score
	candidates := make([]topicCandidate, 0, maxTopicCandidates)
	for _, result := range ranked {
		if result.Score < topicLexicalFloor || result.Score/top < topicLexicalRelative {
			continue
		}
		topic := g.topics[result.ID]
		if topic == nil {
			continue
		}
		score := result.Score
		if topic.participants[event.UserID] {
			score *= 1.08
		}
		if event.Seq > 0 && topic.lastSeq > 0 && event.Seq-topic.lastSeq <= 8 {
			score *= 1.08
		}
		candidates = append(candidates, topicCandidate{id: result.ID, score: score})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].id < candidates[j].id
	})
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.id)
	}
	return ids
}

func (g *topicGraph) isSameLiveTurn(event qq.Event) bool {
	if g.lastLiveEvent == nil || g.lastLiveEvent.UserID != event.UserID || g.lastLiveEvent.Outbound || event.Outbound {
		return false
	}
	if g.lastLiveEvent.At.IsZero() || event.At.IsZero() {
		return true
	}
	gap := event.At.Sub(g.lastLiveEvent.At)
	return gap >= 0 && gap <= 90*time.Second
}

func (g *topicGraph) latestBotTopic() []string {
	var latest *topicState
	for _, topic := range g.topics {
		if !topic.botParticipated {
			continue
		}
		if latest != nil && (topic.botAt.Before(latest.botAt) || topic.botAt.Equal(latest.botAt) && topic.id > latest.id) {
			continue
		}
		latest = topic
	}
	if latest == nil {
		return nil
	}
	return []string{latest.id}
}

func (g *topicGraph) newTopic() *topicState {
	if len(g.topics) >= maxTopicsPerGroup {
		g.retireOldestTopic()
	}
	g.nextID++
	topic := &topicState{
		id:           fmt.Sprintf("topic:%d:%d", g.groupID, g.nextID),
		participants: map[int64]bool{},
		textEventIDs: map[string]bool{},
	}
	g.topics[topic.id] = topic
	return topic
}

func (g *topicGraph) touch(topic *topicState, event qq.Event) {
	if event.UserID != 0 && !event.Outbound {
		topic.addParticipant(event.UserID)
	}
	if !containsString(topic.eventIDs, event.ID) {
		topic.eventIDs = append(topic.eventIDs, event.ID)
		if len(topic.eventIDs) > maxTopicEvents {
			topic.eventIDs = topic.eventIDs[len(topic.eventIDs)-maxTopicEvents:]
		}
	}
	if !event.Recalled && strings.TrimSpace(event.Text) != "" && !event.Outbound {
		topic.addText(event.ID, event.Text)
	}
	if event.Seq > topic.lastSeq {
		topic.lastSeq = event.Seq
	}
	if topic.firstSeq == 0 && event.Seq > 0 {
		topic.firstSeq = event.Seq
	}
	if topic.revision == 0 {
		topic.revision = 1
	}
	if event.At.After(topic.lastAt) {
		topic.lastAt = event.At
	}
	if topic.lastAt.IsZero() {
		topic.lastAt = time.Now()
	}
}

func (topic *topicState) addParticipant(userID int64) {
	if topic.participants[userID] {
		return
	}
	topic.participants[userID] = true
	topic.participantOrder = append(topic.participantOrder, userID)
	if len(topic.participantOrder) <= maxTopicParticipants {
		return
	}
	retired := len(topic.participantOrder) - maxTopicParticipants
	for _, retiredID := range topic.participantOrder[:retired] {
		delete(topic.participants, retiredID)
	}
	topic.participantOrder = topic.participantOrder[retired:]
}

func (g *topicGraph) record(eventID string, topicIDs []string, messageID string) {
	if eventID == "" {
		return
	}
	topicIDs = uniqueTopicIDs(topicIDs)
	if len(topicIDs) == 0 {
		g.removeEventAssignment(eventID)
		return
	}
	if _, exists := g.eventTopics[eventID]; !exists {
		g.eventOrder = append(g.eventOrder, eventID)
	}
	g.eventTopics[eventID] = topicIDs
	g.updateMessageAssignment(eventID, messageID, topicIDs)
	for len(g.eventOrder) > maxTopicAssignments {
		g.removeEventAssignment(g.eventOrder[0])
	}
}

func (g *topicGraph) updateMessageAssignment(eventID, messageID string, topicIDs []string) {
	oldMessageID := g.eventMessageIDs[eventID]
	if oldMessageID != "" && oldMessageID != messageID {
		g.releaseMessageEvent(oldMessageID)
		delete(g.eventMessageIDs, eventID)
	}
	if messageID == "" {
		return
	}
	if oldMessageID != messageID {
		g.eventMessageIDs[eventID] = messageID
		g.messageEvents[messageID]++
	}
	g.messageTopics[messageID] = append([]string(nil), topicIDs...)
}

func (g *topicGraph) removeEventAssignment(eventID string) {
	if messageID := g.eventMessageIDs[eventID]; messageID != "" {
		g.releaseMessageEvent(messageID)
		delete(g.eventMessageIDs, eventID)
	}
	delete(g.eventTopics, eventID)
	for i, existing := range g.eventOrder {
		if existing == eventID {
			g.eventOrder = append(g.eventOrder[:i], g.eventOrder[i+1:]...)
			break
		}
	}
	for _, topic := range g.topics {
		if !containsString(topic.eventIDs, eventID) {
			continue
		}
		for i, existing := range topic.eventIDs {
			if existing == eventID {
				topic.eventIDs = append(topic.eventIDs[:i], topic.eventIDs[i+1:]...)
				break
			}
		}
		topic.removeText(eventID)
	}
}

func (topic *topicState) addText(eventID, text string) {
	if topic.textEventIDs[eventID] {
		return
	}
	topic.textEventIDs[eventID] = true
	topic.textEventOrder = append(topic.textEventOrder, eventID)
	topic.texts = append(topic.texts, text)
	if len(topic.textEventOrder) <= maxTopicEvents {
		return
	}
	retired := len(topic.textEventOrder) - maxTopicEvents
	for _, retiredID := range topic.textEventOrder[:retired] {
		delete(topic.textEventIDs, retiredID)
	}
	topic.textEventOrder = topic.textEventOrder[retired:]
	topic.texts = topic.texts[retired:]
}

func (topic *topicState) removeText(eventID string) {
	for i, existing := range topic.textEventOrder {
		if existing != eventID {
			continue
		}
		delete(topic.textEventIDs, eventID)
		topic.textEventOrder = append(topic.textEventOrder[:i], topic.textEventOrder[i+1:]...)
		topic.texts = append(topic.texts[:i], topic.texts[i+1:]...)
		return
	}
}

func (g *topicGraph) releaseMessageEvent(messageID string) {
	g.messageEvents[messageID]--
	if g.messageEvents[messageID] > 0 {
		return
	}
	delete(g.messageEvents, messageID)
	delete(g.messageTopics, messageID)
}

func (g *topicGraph) retireOldestTopic() {
	var oldestID string
	var oldest time.Time
	for id, topic := range g.topics {
		if oldestID == "" || topic.lastAt.Before(oldest) || topic.lastAt.Equal(oldest) && id < oldestID {
			oldestID = id
			oldest = topic.lastAt
		}
	}
	if oldestID == "" {
		return
	}
	delete(g.topics, oldestID)
	for eventID, ids := range g.eventTopics {
		if !containsString(ids, oldestID) {
			continue
		}
		remaining := make([]string, 0, len(ids)-1)
		for _, id := range ids {
			if id != oldestID {
				remaining = append(remaining, id)
			}
		}
		if len(remaining) == 0 {
			g.removeEventAssignment(eventID)
			continue
		}
		g.eventTopics[eventID] = remaining
		if messageID := g.eventMessageIDs[eventID]; messageID != "" {
			g.messageTopics[messageID] = append([]string(nil), remaining...)
		}
	}
}

func (t *topicTracker) evictOldestGroup() {
	var oldestID int64
	var oldest time.Time
	found := false
	for groupID, graph := range t.groups {
		if !found || graph.lastActive.Before(oldest) || graph.lastActive.Equal(oldest) && groupID < oldestID {
			oldestID = groupID
			oldest = graph.lastActive
			found = true
		}
	}
	if found {
		delete(t.groups, oldestID)
	}
}

func (g *topicGraph) visibleTopicEvents(topic *topicState, visibleTopics map[string]bool, order []string) []string {
	out := make([]string, 0, maxTopicContextEvent)
	for _, eventID := range order {
		if containsString(g.eventTopics[eventID], topic.id) {
			out = append(out, eventID)
		}
	}
	if len(out) > maxTopicContextEvent {
		out = out[len(out)-maxTopicContextEvent:]
	}
	return out
}

func topicStateAt(topic *topicState, now time.Time, activeWindow time.Duration) string {
	if activeWindow > 0 && now.Sub(topic.lastAt) <= activeWindow {
		return "active"
	}
	warmWindow := max(10*time.Minute, 8*activeWindow)
	if now.Sub(topic.lastAt) <= warmWindow {
		return "warm"
	}
	return "dormant"
}

func sortedTopicParticipants(topic *topicState) []int64 {
	out := make([]int64, 0, len(topic.participants))
	for userID := range topic.participants {
		out = append(out, userID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func uniqueTopicIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
