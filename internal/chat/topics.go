package chat

import (
	"fmt"
	"sort"
	"strconv"
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
	eventSeqs       map[string]int64
	eventTimes      map[string]time.Time
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
	summaryRevision  int64
	revision         int64
	botMessageAt     time.Time
	botActionAt      time.Time
}

// botParticipated reports whether any confirmed message or action marks the
// bot as part of this topic.
func (topic *topicState) botParticipated() bool {
	return !topic.botMessageAt.IsZero() || !topic.botActionAt.IsZero()
}

// botLatestAt returns the most recent bot participation time of either kind.
func (topic *topicState) botLatestAt() time.Time {
	if topic.botMessageAt.After(topic.botActionAt) {
		return topic.botMessageAt
	}
	return topic.botActionAt
}

// botActiveWithin reports whether bot participation of either kind falls
// inside the active window.
func (topic *topicState) botActiveWithin(now time.Time, window time.Duration) bool {
	return (!topic.botMessageAt.IsZero() && now.Sub(topic.botMessageAt) <= window) ||
		(!topic.botActionAt.IsZero() && now.Sub(topic.botActionAt) <= window)
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

func (t *topicTracker) Snapshot(groupID int64, activeWindow time.Duration) []conversation.TopicView {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.groups[groupID]
	if g == nil {
		return nil
	}
	out := make([]conversation.TopicView, 0, len(g.topics))
	for _, topic := range g.topics {
		out = append(out, conversation.TopicView{ID: topic.id, Summary: topic.summary, SummaryRevision: topic.summaryRevision, Participants: sortedTopicParticipants(topic), FirstSeq: topic.firstSeq, LastSeq: topic.lastSeq, Revision: topic.revision, LastActiveAt: topic.lastAt, BotMessageAt: topic.botMessageAt, BotActionAt: topic.botActionAt, Status: topicStateAt(topic, time.Now(), activeWindow)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActiveAt.After(out[j].LastActiveAt) })
	return out
}

// EventSnapshot exports the bounded event ownership window for persistence.
func (t *topicTracker) EventSnapshot(groupID int64, limit int) []conversation.TopicEventView {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.groups[groupID]
	if g == nil || limit <= 0 {
		return nil
	}
	start := max(0, len(g.eventOrder)-limit)
	out := make([]conversation.TopicEventView, 0, len(g.eventOrder)-start)
	for _, eventID := range g.eventOrder[start:] {
		ids := g.eventTopics[eventID]
		if len(ids) == 0 {
			continue
		}
		out = append(out, conversation.TopicEventView{TopicID: ids[0], EventID: eventID, MessageID: g.eventMessageIDs[eventID], Seq: g.eventSeqs[eventID], AssignedAt: g.eventTimes[eventID], TopicIDs: append([]string(nil), ids...)})
	}
	return out
}

func (t *topicTracker) TopicIDs(groupID int64, eventIDs []string) map[string][]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string][]string{}
	graph := t.groups[groupID]
	if graph == nil {
		return out
	}
	for _, eventID := range eventIDs {
		if ids := uniqueTopicIDs(graph.eventTopics[eventID]); len(ids) > 0 {
			out[eventID] = ids
		}
	}
	return out
}

func (t *topicTracker) Revision(groupID int64) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.groups[groupID]
	if g == nil {
		return 0
	}
	var revision int64
	for _, topic := range g.topics {
		revision += topic.revision
	}
	return revision
}

// Restore rebuilds stored ownership rows and topic IDs. In-memory topics win
// when the same ID already exists in this process.
func (t *topicTracker) Restore(groupID int64, views []conversation.TopicView, events []conversation.TopicEventView) {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.groups[groupID]
	if g == nil {
		g = &topicGraph{groupID: groupID, topics: map[string]*topicState{}, eventTopics: map[string][]string{}, eventMessageIDs: map[string]string{}, eventSeqs: map[string]int64{}, eventTimes: map[string]time.Time{}, messageEvents: map[string]int{}, messageTopics: map[string][]string{}}
		t.groups[groupID] = g
	}
	for _, view := range views {
		if view.ID == "" || g.topics[view.ID] != nil {
			continue
		}
		topic := &topicState{id: view.ID, participants: map[int64]bool{}, textEventIDs: map[string]bool{}, firstSeq: view.FirstSeq, lastSeq: view.LastSeq, lastAt: view.LastActiveAt, botMessageAt: view.BotMessageAt, botActionAt: view.BotActionAt, summary: view.Summary, summaryRevision: max(view.SummaryRevision, view.Revision), revision: max(view.Revision, 1)}
		for _, id := range view.Participants {
			topic.addParticipant(id)
		}
		g.topics[topic.id] = topic
		if suffix := topicSuffix(topic.id); suffix > g.nextID {
			g.nextID = suffix
		}
	}
	for _, event := range events {
		if event.EventID == "" || len(event.TopicIDs) == 0 {
			continue
		}
		ids := uniqueTopicIDs(event.TopicIDs)
		if _, exists := g.eventTopics[event.EventID]; exists {
			continue
		}
		g.eventTopics[event.EventID] = ids
		g.eventSeqAt(event.EventID, event.Seq, event.AssignedAt)
		if event.MessageID != "" {
			g.eventMessageIDs[event.EventID] = event.MessageID
			g.messageEvents[event.MessageID]++
			g.messageTopics[event.MessageID] = append([]string(nil), ids...)
		}
		g.eventOrder = append(g.eventOrder, event.EventID)
	}
	g.lastActive = time.Now()
}

func topicSuffix(id string) int {
	index := strings.LastIndexByte(id, ':')
	if index < 0 {
		return 0
	}
	n, err := strconv.Atoi(id[index+1:])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (g *topicGraph) eventSeqAt(eventID string, seq int64, at time.Time) {
	g.eventSeqs[eventID] = seq
	g.eventTimes[eventID] = at
}

// ApplySummary installs a committed summary without resetting topic state.
func (t *topicTracker) ApplySummary(groupID int64, summary conversation.TopicSummary) {
	t.mu.Lock()
	defer t.mu.Unlock()
	graph := t.groups[groupID]
	if graph == nil || graph.topics[summary.TopicID] == nil {
		return
	}
	topic := graph.topics[summary.TopicID]
	topic.summary = summary.Summary
	topic.summaryRevision = summary.Revision
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
			eventSeqs:       map[string]int64{},
			eventTimes:      map[string]time.Time{},
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
		if topic.botActiveWithin(now, activeWindow) {
			observation.ActiveBotTopics[topicID] = true
		}
		eventIDs := graph.visibleTopicEvents(topic, visibleTopics, visibleOrder)
		observation.Topics = append(observation.Topics, ConversationTopic{
			ID:                     topic.id,
			State:                  topicStateAt(topic, now, activeWindow),
			Participants:           sortedTopicParticipants(topic),
			EventIDs:               eventIDs,
			BotParticipated:        topic.botParticipated(),
			BotActionParticipated:  !topic.botActionAt.IsZero(),
			Summary:                topic.summary,
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

// markBotAction records a confirmed platform action on a topic: the action
// updates bot_action_at and topic activity but is never archived as a message
// event and does not participate in lexical clustering.
func (t *topicTracker) markBotAction(groupID int64, topicID string, at time.Time) {
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
	if at.IsZero() {
		at = time.Now()
	}
	if at.After(topic.botActionAt) {
		topic.botActionAt = at
	}
	if at.After(topic.lastAt) {
		topic.lastAt = at
	}
}

// topicIDForMessage resolves the primary topic of an archived message, used
// to bind a reaction to the topic of its target message.
func (t *topicTracker) topicIDForMessage(groupID int64, messageID string) string {
	if messageID == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	graph := t.groups[groupID]
	if graph == nil {
		return ""
	}
	if ids := graph.messageTopics[messageID]; len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// topicIDForEvent resolves the primary topic of a visible event, used to bind
// a poke to the topic of its target event.
func (t *topicTracker) topicIDForEvent(groupID int64, eventID string) string {
	if eventID == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	graph := t.groups[groupID]
	if graph == nil {
		return ""
	}
	if ids := graph.eventTopics[eventID]; len(ids) > 0 {
		return ids[0]
	}
	return ""
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
	topic.botMessageAt = event.At
	if topic.botMessageAt.IsZero() {
		topic.botMessageAt = time.Now()
	}
	graph.record(event, []string{topicID})
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
	switch {
	case event.Kind == "poke":
		// A poke has no target event: it never joins or creates a topic.
		return nil
	case event.Kind == "reaction":
		// A reaction binds only to the topic of the message it reacts to and
		// never guesses by recency or similarity.
		if event.TargetMessageID != "" {
			ids = append(ids, g.messageTopics[event.TargetMessageID]...)
		}
		ids = uniqueTopicIDs(ids)
		if len(ids) == 0 {
			return nil
		}
	case event.Recalled:
		if event.MessageID != "" {
			ids = append(ids, g.messageTopics[event.MessageID]...)
		}
	case event.ReplyTo != "":
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
	g.record(event, ids)
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
		if !topic.botParticipated() {
			continue
		}
		if latest != nil && (topic.botLatestAt().Before(latest.botLatestAt()) || topic.botLatestAt().Equal(latest.botLatestAt()) && topic.id > latest.id) {
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

func (g *topicGraph) record(event qq.Event, topicIDs []string) {
	if event.ID == "" {
		return
	}
	topicIDs = uniqueTopicIDs(topicIDs)
	if len(topicIDs) == 0 {
		g.removeEventAssignment(event.ID)
		return
	}
	_, known := g.eventTopics[event.ID]
	if !known {
		g.eventOrder = append(g.eventOrder, event.ID)
	}
	g.eventTopics[event.ID] = topicIDs
	g.eventSeqAt(event.ID, event.Seq, event.At)
	g.updateMessageAssignment(event.ID, event.MessageID, topicIDs)
	if !known {
		for _, id := range topicIDs {
			if topic := g.topics[id]; topic != nil {
				topic.revision = max(topic.revision, 1) + 1
			}
		}
	}
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
	delete(g.eventSeqs, eventID)
	delete(g.eventTimes, eventID)
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
