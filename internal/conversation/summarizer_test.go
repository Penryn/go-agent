package conversation_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/model"
)

type summaryStore struct {
	batch    conversation.SummaryBatch
	saved    []conversation.TopicSummary
	through  int64
	deferred int
	applied  int
	done     bool
}

func (s *summaryStore) SummaryBatch(context.Context, int) (conversation.SummaryBatch, error) {
	if s.done {
		return conversation.SummaryBatch{}, nil
	}
	return s.batch, nil
}
func (s *summaryStore) SaveTopicSummaries(_ context.Context, _ int64, summaries []conversation.TopicSummary, through int64) error {
	s.saved = append(s.saved, summaries...)
	s.through = through
	s.done = true
	return nil
}
func (s *summaryStore) DeferTopicSummary(context.Context, int64) error {
	s.deferred++
	return nil
}

type summaryModel struct {
	content string
	err     error
	input   any
}

func (m *summaryModel) Generate(_ context.Context, _ string, input any, _ []string) (model.Result, error) {
	m.input = input
	return model.Result{Content: m.content}, m.err
}

func batch() conversation.SummaryBatch {
	return conversation.SummaryBatch{GroupID: 1, AfterSeq: 0, Events: []conversation.SummaryEvent{
		{TopicID: "topic:1:1", Revision: 2, EventID: "e1", Seq: 1, Text: "周末去爬山吗"},
		{TopicID: "topic:1:1", Revision: 2, EventID: "e2", Seq: 2, Text: "爬到山顶要多久"},
		{TopicID: "topic:1:2", Revision: 1, EventID: "e3", Seq: 3, Text: "今晚吃火锅"},
	}}
}

func TestSummarizerCommitsEvidenceAndCursor(t *testing.T) {
	store := &summaryStore{batch: batch()}
	summarizer := conversation.TopicSummarizer{Store: store, Model: &summaryModel{content: `{"summaries":[{"topic_id":"topic:1:1","summary":"讨论周末爬山","evidence":["e1","e2"]}]}`}, Batch: 4, Apply: func(conversation.TopicSummary) { store.applied++ }}
	if err := summarizer.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || store.saved[0].TopicID != "topic:1:1" || store.saved[0].SourceSeq != 2 {
		t.Fatalf("unexpected summary: %+v", store.saved)
	}
	if store.through != 3 || store.applied != 1 {
		t.Fatalf("cursor or apply missing: through=%d applied=%d", store.through, store.applied)
	}
}

func TestSummarizerDoesNotAdvanceCursorOnFailure(t *testing.T) {
	store := &summaryStore{batch: batch()}
	summarizer := conversation.TopicSummarizer{Store: store, Model: &summaryModel{err: errors.New("model down")}, Batch: 4}
	if err := summarizer.Tick(context.Background()); err == nil {
		t.Fatal("expected the model failure to surface")
	}
	if store.deferred != 1 || store.through != 0 || len(store.saved) != 0 {
		t.Fatalf("failed summary advanced state: %+v", store)
	}
}

func TestSummarizerSkipsNonTextEventsButAdvances(t *testing.T) {
	store := &summaryStore{batch: conversation.SummaryBatch{GroupID: 1, Events: []conversation.SummaryEvent{{TopicID: "topic:1:1", Revision: 1, EventID: "bot", Seq: 9, Outbound: true}}}}
	summarizer := conversation.TopicSummarizer{Store: store, Model: &summaryModel{err: errors.New("must not be called")}, Batch: 4}
	if err := summarizer.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.through != 9 || len(store.saved) != 0 {
		t.Fatalf("non-text window was not advanced: %+v", store)
	}
}

func TestSummarizerStopsCursorBeforeSkippedEvents(t *testing.T) {
	events := []conversation.SummaryEvent{
		{TopicID: "topic:1:1", Revision: 2, EventID: "e1", Seq: 1, Text: "第一句"},
		{TopicID: "topic:1:1", Revision: 2, EventID: "e2", Seq: 2, Text: "第二句"},
		{TopicID: "topic:1:1", Revision: 2, EventID: "e3", Seq: 3, Text: "第三句"},
		{TopicID: "topic:1:2", Revision: 1, EventID: "e4", Seq: 4, Text: "另一个话题"},
	}
	store := &summaryStore{batch: conversation.SummaryBatch{GroupID: 1, Events: events}}
	generator := &summaryModel{content: `{"summaries":[{"topic_id":"topic:1:1","summary":"前两句","evidence":["e1","e2"]},{"topic_id":"topic:1:2","summary":"另一个话题","evidence":["e4"]}]}`}
	summarizer := conversation.TopicSummarizer{Store: store, Model: generator, Batch: 2}
	if err := summarizer.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(generator.input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "e3:") {
		t.Fatalf("model saw an event outside the window: %s", raw)
	}
	if store.through != 2 {
		t.Fatalf("cursor = %d, want 2 (before the first skipped event)", store.through)
	}
	if len(store.saved) != 2 || store.saved[0].SourceSeq != 2 {
		t.Fatalf("unexpected summaries: %+v", store.saved)
	}
}

func TestSummarizerRejectsEvidenceFromSkippedEvents(t *testing.T) {
	events := []conversation.SummaryEvent{
		{TopicID: "topic:1:1", Revision: 2, EventID: "e1", Seq: 1, Text: "第一句"},
		{TopicID: "topic:1:1", Revision: 2, EventID: "e2", Seq: 2, Text: "第二句"},
		{TopicID: "topic:1:1", Revision: 2, EventID: "e3", Seq: 3, Text: "第三句"},
	}
	store := &summaryStore{batch: conversation.SummaryBatch{GroupID: 1, Events: events}}
	generator := &summaryModel{content: `{"summaries":[{"topic_id":"topic:1:1","summary":"引用没看到的内容","evidence":["e3"]}]}`}
	summarizer := conversation.TopicSummarizer{Store: store, Model: generator, Batch: 2}
	if err := summarizer.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("summary with unseen evidence was saved: %+v", store.saved)
	}
	if store.through != 2 {
		t.Fatalf("cursor = %d, want 2", store.through)
	}
}
