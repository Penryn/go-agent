package workspace

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

func TestStageInputAndCollectOutputs(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.zip")
	if err := os.WriteFile(source, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(filepath.Join(root, "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := manager.Create(context.Background(), "file-task")
	if err != nil {
		t.Fatal(err)
	}
	staged, err := manager.StageInput(context.Background(), ws, source, "input.zip")
	if err != nil {
		t.Fatal(err)
	}
	if staged != filepath.Join(ws.InputDir, "input.zip") {
		t.Fatalf("unexpected staged path %q", staged)
	}
	output := filepath.Join(ws.OutputDir, "result.txt")
	if err := os.WriteFile(output, []byte("result"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts, err := manager.CollectOutputs(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].Name != "result.txt" || artifacts[0].Size != 6 || artifacts[0].SHA256 == "" {
		t.Fatalf("unexpected artifacts: %#v", artifacts)
	}
	segment, err := manager.FileSegment(ws, artifacts[0])
	if err != nil {
		t.Fatal(err)
	}
	if segment.Type != "file" || segment.Data["file"] != output || segment.Data["name"] != "result.txt" {
		t.Fatalf("unexpected file segment: %#v", segment)
	}
	segment, err = manager.FileSegment(ws, Artifact{Name: "escape", Path: source})
	if err == nil || segment.Type != "" {
		t.Fatalf("expected outside artifact rejection: %#v %v", segment, err)
	}
	if err := manager.Cleanup(ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ws.Root); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists: %v", err)
	}
}

func TestStageInputRejectsPathTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(filepath.Join(root, "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := manager.Create(context.Background(), "file-task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StageInput(context.Background(), ws, source, filepath.Join("..", "escape")); err == nil {
		t.Fatal("expected invalid input filename")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StageInput(context.Background(), ws, link, "link"); err == nil || !strings.Contains(err.Error(), "regular local file") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestStageAttachmentDownloadsIntoInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bundle.zip" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("zip-bytes"))
	}))
	defer server.Close()
	manager, err := NewManager(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := manager.Create(context.Background(), "attachment")
	if err != nil {
		t.Fatal(err)
	}
	path, err := manager.StageAttachment(context.Background(), ws, mediadomain.MultimodalAttachment{
		AttachmentID: "a1",
		URL:          fmt.Sprintf("%s/bundle.zip", server.URL),
		Kind:         mediadomain.MediaFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "zip-bytes" || filepath.Base(path) != "bundle.zip" {
		t.Fatalf("unexpected staged attachment %q at %s", content, path)
	}
}
