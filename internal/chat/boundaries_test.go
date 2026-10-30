package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	ctxsnapshot "github.com/phlin/go-agent/internal/context"
	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
)

type revisionStub struct {
	revision int64
	err      error
}

func (s *revisionStub) Ingest(context.Context, *qq.Event) (bool, error)        { return false, nil }
func (s *revisionStub) Recent(context.Context, int64, int) ([]qq.Event, error) { return nil, nil }
func (s *revisionStub) IsBotReply(context.Context, int64, string) (bool, error) {
	return false, nil
}
func (s *revisionStub) BeginAttempt(context.Context, Attempt) (bool, error) { return false, nil }
func (s *revisionStub) UpdateAttempt(context.Context, Attempt) error        { return nil }
func (s *revisionStub) FinishAttempt(context.Context, Attempt, qq.Event, []string) error {
	return nil
}
func (s *revisionStub) SaveDelivery(context.Context, string, string, string) error { return nil }
func (s *revisionStub) ConfirmAction(context.Context, Attempt, ActionRecord, []string) error {
	return nil
}
func (s *revisionStub) SavePendingMemory(context.Context, string, int64, []memory.NoteSnapshot) error {
	return nil
}
func (s *revisionStub) DiscardPendingMemory(context.Context, string, string) error { return nil }
func (s *revisionStub) RepliesSince(context.Context, int64, time.Time) (int, time.Time, error) {
	return 0, time.Time{}, nil
}
func (s *revisionStub) MemoryRevision(context.Context, int64) (int64, error) { return 0, nil }
func (s *revisionStub) StickerRevision(context.Context) (int64, error)       { return s.revision, s.err }

func TestContextCacheKeyRequiresStickerRevision(t *testing.T) {
	store := &revisionStub{revision: 3}
	r := New(context.Background(), &Runtime{Store: store, Memes: &meme.Service{}, Config: Config{Cache: CacheTTL{Context: time.Minute}}})
	t.Cleanup(r.Close)
	admission := ctxsnapshot.AdmissionSnapshot{GroupID: 1, Batch: []qq.Event{{ID: "e", UserID: 10}}, Query: "query"}
	if key := r.contextCacheKey(context.Background(), admission, "v1", 0); key == "" {
		t.Fatal("expected a cache key with a readable revision")
	}
	store.err = errors.New("db down")
	if key := r.contextCacheKey(context.Background(), admission, "v1", 0); key != "" {
		t.Fatal("context cache key used a failed asset revision")
	}
}
