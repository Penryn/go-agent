package learning

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	memsvc "github.com/phlin/go-agent/internal/application/memory"
	"github.com/phlin/go-agent/internal/application/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	"github.com/phlin/go-agent/internal/testsupport"
)

type fixedLLM struct {
	response string
	called   int
}

func (f *fixedLLM) Generate(context.Context, string) (string, error) {
	f.called++
	return f.response, nil
}

type recordingTaskSubmitter struct {
	kind    string
	key     string
	payload []byte
}

func (r *recordingTaskSubmitter) Enqueue(_ context.Context, kind, key string, payload []byte) error {
	r.kind, r.key, r.payload = kind, key, payload
	return nil
}

func newLearningTestService(t *testing.T, llm LLMProvider, outbox ports.TaskSubmitter) (*Service, *postgresstore.Store) {
	t.Helper()
	store := testsupport.NewStore(t)
	service, err := New(context.Background(), store, store, memsvc.New(store), WithLLM(llm), WithOutbox(outbox))
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func TestProcessBatchUsesRealEvidenceAndWritesRetrievableMemory(t *testing.T) {
	llm := &fixedLLM{response: `[{"subject_kind":"user","subject_id":"7","type":"semantic","subtype":"preference","content":"用户7喜欢咖啡","predicate":"likes_topic","normalized_value":"咖啡","evidence_event_ids":["0"],"confidence":0.95}]`}
	service, store := newLearningTestService(t, llm, nil)
	event := conversationdomain.ConversationEvent{EventID: "evt-1", GroupID: 1, UserID: 7, MessageID: "msg-1", Text: "我喜欢咖啡", TimestampUnix: time.Now().Unix()}
	if err := store.ArchiveEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessBatch(context.Background(), 1, []string{"evt-1"}); err != nil {
		t.Fatal(err)
	}
	if llm.called != 1 {
		t.Fatalf("expected one extraction call, got %d", llm.called)
	}
	records, err := store.QueryMemories(context.Background(), ports.MemoryQuery{GroupID: 1, UserID: 7, Query: "咖啡", TopK: 5})
	if err != nil || len(records) != 1 || records[0].Content != "用户7喜欢咖啡" {
		all, allErr := store.QueryMemories(context.Background(), ports.MemoryQuery{GroupID: 1, UserID: 7, TopK: 5})
		t.Fatalf("memory was not written through the retrieval path: records=%+v all=%+v err=%v allErr=%v", records, all, err, allErr)
	}
}

func TestProcessBatchRejectsFabricatedEvidence(t *testing.T) {
	llm := &fixedLLM{response: `[{"subject_kind":"user","subject_id":"7","type":"semantic","content":"伪造","evidence_event_ids":["missing"],"confidence":0.95}]`}
	service, store := newLearningTestService(t, llm, nil)
	event := conversationdomain.ConversationEvent{EventID: "evt-1", GroupID: 1, UserID: 7, Text: "普通消息", TimestampUnix: time.Now().Unix()}
	if err := store.ArchiveEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessBatch(context.Background(), 1, []string{"evt-1"}); err != nil {
		t.Fatal(err)
	}
	records, err := store.QueryMemories(context.Background(), ports.MemoryQuery{GroupID: 1, UserID: 7, TopK: 5})
	if err != nil || len(records) != 0 {
		t.Fatalf("fabricated evidence became memory: records=%+v err=%v", records, err)
	}
}

func TestProcessBatchSkipsOutboundFromExtraction(t *testing.T) {
	llm := &fixedLLM{response: `[{"subject_kind":"user","subject_id":"9","type":"semantic","content":"bot自述","evidence_event_ids":["0"],"confidence":0.95}]`}
	service, store := newLearningTestService(t, llm, nil)
	event := conversationdomain.ConversationEvent{EventID: "evt-out", Origin: "outbound", GroupID: 1, UserID: 9, Text: "bot自述", TimestampUnix: time.Now().Unix()}
	if err := store.ArchiveEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessBatch(context.Background(), 1, []string{"evt-out"}); err != nil {
		t.Fatal(err)
	}
	if llm.called != 0 {
		t.Fatalf("outbound event reached extractor: calls=%d", llm.called)
	}
}

func TestScanAndScheduleUsesFixedEventIDs(t *testing.T) {
	outbox := &recordingTaskSubmitter{}
	service, store := newLearningTestService(t, &fixedLLM{}, outbox)
	for i, id := range []string{"evt-a", "evt-b"} {
		if err := store.ArchiveEvent(context.Background(), conversationdomain.ConversationEvent{
			EventID: id, GroupID: 2, UserID: int64(i + 1), Text: "消息", TimestampUnix: int64(100 + i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.ScanAndSchedule(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	var task BatchTask
	if err := json.Unmarshal(outbox.payload, &task); err != nil {
		t.Fatal(err)
	}
	if outbox.kind != "learning_extract" || task.GroupID != 2 || len(task.EventIDs) != 2 || task.EventIDs[0] != "evt-a" {
		t.Fatalf("unexpected learning task: kind=%s task=%+v", outbox.kind, task)
	}
}

func TestCandidateIntentIsIdempotentForSameEvidence(t *testing.T) {
	base := &memorydomain.MemoryCandidate{
		Scope: "group:1", SubjectKind: memorydomain.SubjectKindUser, SubjectID: "7",
		Type: memorydomain.MemoryTypeSemantic, Content: "喜欢咖啡", EvidenceEventIDs: []string{"evt-1"},
	}
	first := candidateIntent(base)
	second := candidateIntent(base)
	if first.MemoryID != second.MemoryID || first.SourceEventID != "evt-1" {
		t.Fatalf("candidate intent is not stable: first=%+v second=%+v", first, second)
	}
}
