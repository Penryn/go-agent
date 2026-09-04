package learning

import (
	"context"
	"strings"
	"testing"
	"time"

	memsvc "github.com/phlin/go-agent/internal/application/memory"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	"github.com/phlin/go-agent/internal/testsupport"
)

func TestRun(t *testing.T) {
	store := testsupport.NewStore(t)
	service, err := New(context.Background(), store, store, memsvc.New(store))
	if err != nil {
		t.Fatalf("new learning service: %v", err)
	}

	output, err := service.Run(context.Background(), Input{
		GroupID: 1,
		Events: []conversationdomain.ConversationEvent{
			{Text: "离谱", UserID: 1},
			{Text: "离谱", UserID: 2},
			{Text: "离谱", UserID: 1},
			{Text: "今天正常", UserID: 3},
		},
	})
	if err != nil {
		t.Fatalf("run learning service: %v", err)
	}
	if len(output.Candidates) == 0 || output.Candidates[0].Value != "离谱" {
		t.Fatalf("unexpected candidates: %#v", output.Candidates)
	}
}

func TestLearnGroupAdvancesDurableWatermark(t *testing.T) {
	ctx := context.Background()
	store := testsupport.NewStore(t)
	service, err := New(ctx, store, store, memsvc.New(store))
	if err != nil {
		t.Fatalf("new learning service: %v", err)
	}

	for i := 0; i < 10; i++ {
		event := conversationdomain.ConversationEvent{
			EventID:       "event-" + string(rune('a'+i)),
			GroupID:       1,
			UserID:        int64(i % 3),
			MessageID:     "message-" + string(rune('a'+i)),
			Text:          "离谱",
			TimestampUnix: time.Unix(100+int64(i), 0).Unix(),
		}
		if err := store.ArchiveEvent(ctx, event); err != nil {
			t.Fatalf("archive event: %v", err)
		}
	}

	if err := service.learnGroup(ctx, 1); err != nil {
		t.Fatalf("learn group: %v", err)
	}
	watermark, err := store.GetLearningWatermark(ctx, 1, "learning_extract")
	if err != nil {
		t.Fatalf("get watermark: %v", err)
	}
	if watermark.EventID != "event-j" {
		t.Fatalf("watermark event = %q, want event-j", watermark.EventID)
	}
	if err := service.learnGroup(ctx, 1); err != nil {
		t.Fatalf("relearn group: %v", err)
	}
	next, err := store.GetLearningWatermark(ctx, 1, "learning_extract")
	if err != nil {
		t.Fatalf("get second watermark: %v", err)
	}
	if next.EventID != watermark.EventID || !next.OccurredAt.Equal(watermark.OccurredAt) {
		t.Fatalf("watermark changed without new facts: before=%+v after=%+v", watermark, next)
	}
}

func TestLearnGroupAdvancesWatermarkForSmallBatch(t *testing.T) {
	ctx := context.Background()
	store := testsupport.NewStore(t)
	service, err := New(ctx, store, store, memsvc.New(store))
	if err != nil {
		t.Fatalf("new learning service: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := store.ArchiveEvent(ctx, conversationdomain.ConversationEvent{
			EventID: "small-" + string(rune('a'+i)), GroupID: 2, UserID: int64(i + 1),
			TimestampUnix: int64(200 + i), Text: "普通消息",
		}); err != nil {
			t.Fatalf("archive event: %v", err)
		}
	}
	if err := service.ProcessGroup(ctx, 2); err != nil {
		t.Fatalf("process small batch: %v", err)
	}
	watermark, err := store.GetLearningWatermark(ctx, 2, "learning_extract")
	if err != nil || watermark.EventID != "small-b" {
		t.Fatalf("watermark = %+v, err=%v", watermark, err)
	}
}

func TestMergeCandidateEvidenceIsIdempotent(t *testing.T) {
	base := memorydomain.LearningCandidate{ID: "c", EvidenceCount: 3, ExampleEventIDs: []string{"a", "b"}, CreatedAt: time.Now()}
	merged := mergeCandidateEvidence(base, memorydomain.LearningCandidate{ID: "c", EvidenceCount: 3, ExampleEventIDs: []string{"b", "c"}})
	if merged.EvidenceCount != 4 || len(merged.ExampleEventIDs) != 3 {
		t.Fatalf("merged candidate = %+v", merged)
	}
	retry := mergeCandidateEvidence(merged, memorydomain.LearningCandidate{ID: "c", EvidenceCount: 3, ExampleEventIDs: []string{"b", "c"}})
	if retry.EvidenceCount != merged.EvidenceCount {
		t.Fatalf("retry changed evidence count: before=%d after=%d", merged.EvidenceCount, retry.EvidenceCount)
	}
}

func TestRunExtractsBehaviorLearningSignals(t *testing.T) {
	output, err := extractCandidates(context.Background(), Input{GroupID: 1, Events: []conversationdomain.ConversationEvent{
		{EventID: "behavior-1", UserID: 7, Text: "以后别在晚上@我"},
		{EventID: "behavior-2", UserID: 7, Text: "不对，应该是周末再提醒"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, candidate := range output.Candidates {
		seen[candidate.Kind] = true
		if strings.Contains(candidate.Kind, "behavior") && len(candidate.ExampleEventIDs) == 0 {
			t.Fatalf("behavior candidate lost evidence: %+v", candidate)
		}
	}
	if !seen["behavior_rule"] || !seen["behavior_correction"] {
		t.Fatalf("behavior signals were not extracted: %+v", output.Candidates)
	}
}
