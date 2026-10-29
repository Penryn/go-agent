package meme

import (
	"context"
	"errors"
	"testing"
)

type revisionOnly struct {
	revision int64
	err      error
}

func (r revisionOnly) StickerRevision(context.Context) (int64, error) {
	return r.revision, r.err
}

func TestAssetRevisionFailureDisablesCache(t *testing.T) {
	if _, ok := assetRevision(context.Background(), revisionOnly{err: errors.New("db down")}); ok {
		t.Fatal("failed revision must not be usable as a cache key")
	}
	if _, ok := assetRevision(context.Background(), struct{}{}); ok {
		t.Fatal("a store without revisions must bypass the cache")
	}
	revision, ok := assetRevision(context.Background(), revisionOnly{revision: 7})
	if !ok || revision != 7 {
		t.Fatalf("revision = %d ok=%v", revision, ok)
	}
}
