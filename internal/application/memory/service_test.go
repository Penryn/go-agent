package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/application/ports"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

type recordingOutbox struct {
	kind string
	key  string
	body []byte
}

func (o *recordingOutbox) Enqueue(_ context.Context, kind, key string, body []byte) error {
	o.kind, o.key, o.body = kind, key, append([]byte(nil), body...)
	return nil
}

type recordingVectorStore struct{ records []memorydomain.MemoryRecord }

func (s *recordingVectorStore) StoreMemory(_ context.Context, record memorydomain.MemoryRecord) error {
	s.records = append(s.records, record)
	return nil
}

func (s *recordingVectorStore) SearchMemories(context.Context, string, int64, int64, int, float64) ([]memorydomain.MemoryRecord, error) {
	return nil, nil
}

func TestMarkIntentAndQuery(t *testing.T) {
	store := inmemory.NewStore()
	service := New(store)

	record, err := service.MarkIntent(context.Background(), WriteIntent{
		Scope:      "group:1",
		MemoryType: "preference",
		Subject:    "群话题",
		Content:    "这个群喜欢聊表情包",
		Importance: 0.8,
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("mark intent: %v", err)
	}

	records, err := store.QueryMemories(context.Background(), ports.MemoryQuery{
		GroupID: 1,
		Query:   "表情包",
		TopK:    3,
	})
	if err != nil {
		t.Fatalf("query memory: %v", err)
	}
	if len(records) == 0 || records[0].MemoryID != record.MemoryID {
		t.Fatalf("unexpected records: %#v", records)
	}
}

func TestMarkIntentEnqueuesVectorSync(t *testing.T) {
	store := inmemory.NewStore()
	outbox := &recordingOutbox{}
	vector := &recordingVectorStore{}
	service := New(store, WithVectorStore(vector), WithOutbox(outbox))
	record, err := service.MarkIntent(context.Background(), WriteIntent{MemoryID: "memory-1", Scope: "group:1", MemoryType: "topic", Subject: "topic", Content: "content"})
	if err != nil {
		t.Fatalf("mark intent: %v", err)
	}
	if outbox.kind != "memory_vector_index" || !strings.HasPrefix(outbox.key, record.MemoryID+":") {
		t.Fatalf("unexpected outbox envelope: kind=%q key=%q", outbox.kind, outbox.key)
	}
	var got memorydomain.MemoryRecord
	if err := json.Unmarshal(outbox.body, &got); err != nil || got.MemoryID != record.MemoryID {
		t.Fatalf("unexpected payload: record=%+v err=%v", got, err)
	}
	if len(vector.records) != 0 {
		t.Fatal("vector store should be invoked by outbox handler, not inline")
	}
}
