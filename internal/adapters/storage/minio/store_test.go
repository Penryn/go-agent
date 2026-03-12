package miniostore

import (
	"context"
	"strings"
	"testing"

	"github.com/phlin/go-agent/internal/config"
)

func TestStoreIntegration(t *testing.T) {
	ctx := context.Background()
	store, err := New(config.MinIOConfig{
		Endpoint:  "127.0.0.1:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin123",
		Bucket:    "qqbot-media-test",
	})
	if err != nil {
		t.Skipf("minio unavailable: %v", err)
	}

	if err := store.EnsureBucket(ctx); err != nil {
		if isMinIOCapacityError(err) {
			t.Skipf("minio unavailable for writes: %v", err)
		}
		t.Fatalf("ensure bucket: %v", err)
	}

	if _, err := store.PutObject(ctx, "tests/hello.txt", []byte("hello world"), "text/plain"); err != nil {
		if isMinIOCapacityError(err) {
			t.Skipf("minio unavailable for writes: %v", err)
		}
		t.Fatalf("put object: %v", err)
	}

	info, err := store.StatObject(ctx, "tests/hello.txt")
	if err != nil {
		t.Fatalf("stat object: %v", err)
	}
	if info.Size != int64(len("hello world")) {
		t.Fatalf("unexpected object size: %d", info.Size)
	}
}

func isMinIOCapacityError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "minimum free drive threshold")
}
