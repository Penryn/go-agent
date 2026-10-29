package memory_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/model"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/replay"
)

// learningModel scripts the two learning stages and records what each stage
// actually received.
type learningModel struct {
	mu             sync.Mutex
	extract        []string
	reconcile      []string
	extractInputs  []map[string]any
	reconcileInput []map[string]any
	calls          int
}

type staticTopics map[string][]string

func (s staticTopics) TopicIDs(_ context.Context, _ int64, eventIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, eventID := range eventIDs {
		if topicIDs, ok := s[eventID]; ok {
			out[eventID] = append([]string(nil), topicIDs...)
		}
	}
	return out, nil
}

func (m *learningModel) Generate(_ context.Context, system string, input any, _ []string) (model.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	values, ok := input.(map[string]any)
	if !ok {
		return model.Result{}, errors.New("learning input is not an object")
	}
	var queue *[]string
	switch system {
	case memory.ExtractPrompt:
		m.extractInputs = append(m.extractInputs, values)
		queue = &m.extract
	case memory.ReconcilePrompt:
		m.reconcileInput = append(m.reconcileInput, values)
		queue = &m.reconcile
	default:
		return model.Result{}, fmt.Errorf("unexpected prompt: %s", system)
	}
	if len(*queue) == 0 {
		return model.Result{}, errors.New("scripted response exhausted")
	}
	content := (*queue)[0]
	*queue = (*queue)[1:]
	return model.Result{Content: content, Usage: model.Usage{Input: 10, Output: 5}}, nil
}

func ingest(t *testing.T, store *replay.Store, id string, group, user int64, text string) qq.Event {
	t.Helper()
	event := qq.Event{ID: id, MessageID: id, GroupID: group, UserID: user, Text: text, At: time.Now()}
	if ok, err := store.Ingest(context.Background(), &event); err != nil || !ok {
		t.Fatalf("ingest %s: ok=%v err=%v", id, ok, err)
	}
	return event
}

func TestLearningExtractsThenReconcilesOnlyMatchedIdentities(t *testing.T) {
	ctx := context.Background()
	store := replay.NewStore()
	// A same-identity note in another group must never reach the reconciler.
	if err := store.SaveNotes(ctx, 2, []memory.Note{{ID: "note-foreign", GroupID: 2, Subject: 10, Kind: "preference", Key: "nickname", Text: "另一个群的小林", State: "active", Updated: time.Now()}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveNotes(ctx, 1, []memory.Note{{ID: "note-old", GroupID: 1, Subject: 10, Kind: "preference", Key: "nickname", Text: "称呼小林", State: "forgotten", Evidence: []memory.Evidence{{EventID: "old-evidence", Quote: "旧称呼"}}, Updated: time.Now().Add(-time.Hour)}}, nil); err != nil {
		t.Fatal(err)
	}
	first := ingest(t, store, "e1", 1, 10, "以后叫我小林")

	model := &learningModel{
		extract: []string{`{"candidates":[{"subject":10,"kind":"preference","key":"nickname","text":"称呼小林","evidence":[{"event_id":"e1","quote":"以后叫我小林"}],"confidence":0.8}]}`},
		// The candidate matches a forgotten note; a later explicit message is
		// allowed to reactivate it, and the server binds the stored ID/version.
		reconcile: []string{`{"decisions":[{"candidate":0,"action":"update","text":"称呼小林"}]}`},
	}
	service := memory.Service{Store: store, Model: model, TopK: 3}
	if err := service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(model.extractInputs) != 1 || len(model.extractInputs[0]) != 3 || model.extractInputs[0]["events"] == nil || model.extractInputs[0]["event_topics"] == nil {
		t.Fatalf("extraction saw more than this batch: %+v", model.extractInputs)
	}
	existing, _ := model.reconcileInput[0]["existing"].([]memory.Note)
	if len(existing) != 1 || existing[0].ID != "note-old" || existing[0].State != "forgotten" {
		t.Fatalf("reconciler identity window = %+v", existing)
	}
	notes, err := store.ActiveNotes(ctx, 1, 10)
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes = %+v err=%v", notes, err)
	}
	note := notes[0]
	if note.ID != "note-old" || note.Text != "称呼小林" || note.State != "active" || note.SourceSeq != first.Seq || note.Confidence != 0.8 {
		t.Fatalf("reconciled note = %+v", note)
	}

	// An empty extraction is still a successful pass: the cursor advances and
	// the reconciler is not called.
	ingest(t, store, "e2", 1, 10, "今天天气不错")
	model.extract = []string{`{"candidates":[]}`}
	if err := service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(model.reconcileInput) != 1 {
		t.Fatal("reconciler ran without candidates")
	}
	history, _ := model.extractInputs[1]["history"].([]qq.Event)
	if len(history) != 1 || history[0].ID != first.ID {
		t.Fatalf("learning history = %+v", history)
	}
	batch, _, err := store.LearningBatch(ctx, 40)
	if err != nil || len(batch) != 0 {
		t.Fatalf("learning cursor did not advance: %+v err=%v", batch, err)
	}
	runs := store.LearningRuns()
	if len(runs) != 2 || runs[0].Status != "succeeded" || runs[0].CandidateCount != 1 || runs[0].ChangeCount != 1 || runs[0].ExtractTokens != 15 || runs[0].ReconcileTokens != 15 || runs[1].CandidateCount != 0 || runs[1].ReconcileTokens != 0 {
		t.Fatalf("learning runs = %+v", runs)
	}
}

func TestLearningDropsDuplicateCandidatesWithoutBlockingBatch(t *testing.T) {
	ctx := context.Background()
	store := replay.NewStore()
	ingest(t, store, "e1", 1, 10, "以后叫我小林")
	candidateA := `{"subject":10,"kind":"preference","key":" Nick ","text":"称呼小林","evidence":[{"event_id":"e1","quote":"以后叫我小林"}]}`
	candidateB := `{"subject":10,"kind":"preference","key":"nick","text":"称呼小林","evidence":[{"event_id":"e1","quote":"以后叫我小林"}]}`
	model := &learningModel{
		extract:   []string{`{"candidates":[` + candidateA + `,` + candidateB + `]}`},
		reconcile: []string{`{"decisions":[{"candidate":0,"action":"create","text":"称呼小林"}]}`},
	}
	service := memory.Service{Store: store, Model: model, TopK: 3}
	if err := service.Tick(ctx); err != nil {
		t.Fatalf("duplicate candidates blocked batch: %v", err)
	}
	if notes, _ := store.ActiveNotes(ctx, 1, 10); len(notes) != 1 {
		t.Fatalf("duplicate candidates should leave one accepted note: %+v", notes)
	}
	batch, _, err := store.LearningBatch(ctx, 40)
	if err != nil || len(batch) != 0 {
		t.Fatalf("successful partial batch did not advance the cursor: %+v err=%v", batch, err)
	}
	runs := store.LearningRuns()
	if len(runs) != 1 || runs[0].Status != "succeeded" || runs[0].RejectedCount != 1 || runs[0].CandidateCount != 1 || runs[0].ChangeCount != 1 {
		t.Fatalf("learning run = %+v", runs)
	}
}

func TestEpisodeRequiresStableTopic(t *testing.T) {
	ctx := context.Background()
	store := replay.NewStore()
	e := ingest(t, store, "e1", 1, 10, "记住周末去爬山")
	service := memory.Service{Store: store, TopicResolver: staticTopics{}, TopK: 3}
	base := memory.Change{Subject: 10, Kind: "episode", Key: "climb", Text: "周末爬山", Evidence: []memory.Evidence{{EventID: e.ID, Quote: "周末去爬山"}}}
	if err := service.Apply(ctx, 1, []qq.Event{e}, []memory.Change{base}, nil); err == nil {
		t.Fatal("topic-less episode was accepted")
	}
	// The background path is subject to the same rule.
	progress := memory.Progress{GroupID: 1, After: 0, Until: e.Seq}
	if err := service.Apply(ctx, 1, []qq.Event{e}, []memory.Change{base}, &progress); err == nil {
		t.Fatal("background learning accepted a topic-less episode")
	}
	service.TopicResolver = staticTopics{e.ID: []string{"topic:1"}}
	if err := service.Apply(ctx, 1, []qq.Event{e}, []memory.Change{base}, nil); err != nil {
		t.Fatal(err)
	}
	notes, err := store.ActiveNotes(ctx, 1, 10)
	if err != nil || len(notes) != 1 || notes[0].TopicID != "topic:1" {
		t.Fatalf("episode = %+v err=%v", notes, err)
	}
	// An existing episode cannot be rewritten under another topic.
	wrongTopic := base
	wrongTopic.ID, wrongTopic.Version = notes[0].ID, notes[0].Version
	wrongTopic.TopicID = "topic:2"
	if err := service.Apply(ctx, 1, []qq.Event{e}, []memory.Change{wrongTopic}, nil); err == nil {
		t.Fatalf("episode topic was rewritten: %v", err)
	}
	if notes, _ := store.ActiveNotes(ctx, 1, 10); notes[0].TopicID != "topic:1" {
		t.Fatalf("stored episode topic changed: %+v", notes[0])
	}
	// An ID-less update must bind the server topic before looking up the
	// existing identity, otherwise it would try to create a second episode.
	second := ingest(t, store, "e2", 1, 10, "记住周末去爬山，改成山顶集合")
	service.TopicResolver = staticTopics{e.ID: []string{"topic:1"}, second.ID: []string{"topic:1"}}
	idless := memory.Change{Subject: 10, Kind: "episode", Key: "climb", Text: "周末去山顶集合", Evidence: []memory.Evidence{{EventID: second.ID, Quote: "改成山顶集合"}}}
	if err := service.Apply(ctx, 1, []qq.Event{second}, []memory.Change{idless}, nil); err != nil {
		t.Fatalf("ID-less episode update: %v", err)
	}
	notes, _ = store.ActiveNotes(ctx, 1, 10)
	if len(notes) != 1 || notes[0].TopicID != "topic:1" || notes[0].Version != 2 || notes[0].Text != "周末去山顶集合" {
		t.Fatalf("ID-less episode update created a duplicate: %+v", notes)
	}
	crossTopicEvent := ingest(t, store, "e3", 1, 10, "记住另一件爬山的事")
	service.TopicResolver = staticTopics{e.ID: []string{"topic:1"}, second.ID: []string{"topic:1"}, crossTopicEvent.ID: []string{"topic:2"}}
	crossTopic := memory.Change{Subject: 10, Kind: "episode", Key: "climb-cross", TopicID: "topic:1", Text: "跨话题经历", Evidence: []memory.Evidence{{EventID: e.ID, Quote: "周末去爬山"}, {EventID: crossTopicEvent.ID, Quote: "另一件爬山的事"}}}
	if err := service.Apply(ctx, 1, []qq.Event{e, crossTopicEvent}, []memory.Change{crossTopic}, nil); err == nil {
		t.Fatal("episode with evidence from different topics was accepted")
	}
	// Extraction must reject a topic-less episode before reconciliation.
	other := replay.NewStore()
	otherEvent := ingest(t, other, "e2", 1, 10, "记住周末去爬山")
	model := &learningModel{
		extract:   []string{`{"candidates":[{"subject":10,"kind":"episode","key":"climb","text":"周末爬山","evidence":[{"event_id":"e2","quote":"周末去爬山"}]}]}`},
		reconcile: []string{`{"decisions":[{"candidate":0,"action":"create","text":"周末爬山"}]}`},
	}
	learning := memory.Service{Store: other, Model: model, TopicResolver: staticTopics{otherEvent.ID: []string{"topic:1"}}, TopK: 3}
	if err := learning.Tick(ctx); err != nil {
		t.Fatalf("background episode topic binding failed: %v", err)
	}
	notes, err = other.ActiveNotes(ctx, 1, 10)
	if err != nil || len(notes) != 1 || notes[0].TopicID != "topic:1" {
		t.Fatalf("background episode topic = %+v err=%v", notes, err)
	}
}

func TestApplyNormalizesIdentity(t *testing.T) {
	ctx := context.Background()
	store := replay.NewStore()
	event := ingest(t, store, "e1", 1, 10, "以后叫我阿林")
	service := memory.Service{Store: store, TopK: 4, Budgets: memory.RecallBudgets{Preference: 6, Member: 8, Episode: 6}}
	change := memory.Change{Subject: 10, Kind: "preference", Key: "  NickName ", TopicID: "  topic:1  ", Text: "称呼阿林", Evidence: []memory.Evidence{{EventID: event.ID, Quote: "以后叫我阿林"}}}
	if err := service.Apply(ctx, 1, []qq.Event{event}, []memory.Change{change}, nil); err != nil {
		t.Fatal(err)
	}
	notes, err := store.ActiveNotes(ctx, 1, 10)
	if err != nil || len(notes) != 1 || notes[0].Key != "nickname" || notes[0].TopicID != "topic:1" {
		t.Fatalf("stored note = %+v err=%v", notes, err)
	}
	update := memory.Change{ID: notes[0].ID, Version: notes[0].Version, Subject: 10, Kind: "preference", Key: "NICKNAME", TopicID: "topic:1", Text: "称呼阿林", Evidence: []memory.Evidence{{EventID: event.ID, Quote: "以后叫我阿林"}}}
	if err := service.Apply(ctx, 1, []qq.Event{event}, []memory.Change{update}, nil); err != nil {
		t.Fatal(err)
	}
	notes, _ = store.ActiveNotes(ctx, 1, 10)
	if len(notes) != 1 || notes[0].Version != 2 {
		t.Fatalf("normalized update forked a note: %+v", notes)
	}
}

func TestApplyValidatesConfidenceAndValidity(t *testing.T) {
	ctx := context.Background()
	store := replay.NewStore()
	event := ingest(t, store, "e1", 1, 10, "记住我对花生过敏")
	service := memory.Service{Store: store, TopK: 3}
	base := memory.Change{Subject: 10, Kind: "member", Key: "allergy", Text: "对花生过敏", Evidence: []memory.Evidence{{EventID: event.ID, Quote: "对花生过敏"}}}
	bad := float32(1.5)
	withBadConfidence := base
	withBadConfidence.Confidence = &bad
	if err := service.Apply(ctx, 1, []qq.Event{event}, []memory.Change{withBadConfidence}, nil); err == nil {
		t.Fatal("out-of-range confidence accepted")
	}
	nan := float32(0)
	nan = nan / nan
	withNaN := base
	withNaN.Confidence = &nan
	if err := service.Apply(ctx, 1, []qq.Event{event}, []memory.Change{withNaN}, nil); err == nil {
		t.Fatal("NaN confidence accepted")
	}
	from, to := time.Now().Add(time.Hour), time.Now()
	withBadWindow := base
	withBadWindow.ValidFrom, withBadWindow.ValidTo = &from, &to
	if err := service.Apply(ctx, 1, []qq.Event{event}, []memory.Change{withBadWindow}, nil); err == nil {
		t.Fatal("reversed validity interval accepted")
	}
	duplicate := []memory.Change{base, base}
	if err := service.Apply(ctx, 1, []qq.Event{event}, duplicate, nil); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("same-batch conflict: %v", err)
	}
	if notes, _ := store.ActiveNotes(ctx, 1, 10); len(notes) != 0 {
		t.Fatalf("rejected batch wrote notes: %+v", notes)
	}
}
