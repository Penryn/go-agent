// Package workspace manages isolated local workspaces for file-processing tasks.
//
// A workspace is the only filesystem area that a Codex task should receive.
// Inputs are copied into input/, and files intended for delivery must be
// written below output/. The manager deliberately does not execute archives or
// trust paths returned by an agent.
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

const (
	defaultMaxInputBytes  int64 = 512 << 20
	defaultMaxOutputBytes int64 = 512 << 20
)

type Manager struct {
	root           string
	maxInputBytes  int64
	maxOutputBytes int64
	maxOutputFiles int
	httpClient     *http.Client
}

type Workspace struct {
	ID        string
	Root      string
	InputDir  string
	OutputDir string
}

// Task is the ephemeral filesystem context for one inbound file-processing
// turn. It is carried through context to the planner and Codex tool; it is
// intentionally not persisted in conversation history.
type Task struct {
	Manager    *Manager
	Workspace  Workspace
	InputFiles []string
}

type taskContextKey struct{}

func WithTask(ctx context.Context, task Task) context.Context {
	return context.WithValue(ctx, taskContextKey{}, task)
}

func TaskFromContext(ctx context.Context) (Task, bool) {
	task, ok := ctx.Value(taskContextKey{}).(Task)
	return task, ok && task.Workspace.Root != ""
}

type Artifact struct {
	Name      string
	Path      string
	Size      int64
	SHA256    string
	MediaType string
}

func NewManager(root string) (*Manager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("workspace root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}
	return &Manager{
		root:           absolute,
		maxInputBytes:  defaultMaxInputBytes,
		maxOutputBytes: defaultMaxOutputBytes,
		maxOutputFiles: 32,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (m *Manager) Create(ctx context.Context, id string) (Workspace, error) {
	if err := ctx.Err(); err != nil {
		return Workspace{}, err
	}
	prefix := "task-"
	if safeID := safeName(id); safeID != "" {
		prefix += safeID + "-"
	}
	root, err := os.MkdirTemp(m.root, prefix)
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	inputDir := filepath.Join(root, "input")
	outputDir := filepath.Join(root, "output")
	if err := os.MkdirAll(inputDir, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return Workspace{}, fmt.Errorf("create input directory: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return Workspace{}, fmt.Errorf("create output directory: %w", err)
	}
	return Workspace{ID: filepath.Base(root), Root: root, InputDir: inputDir, OutputDir: outputDir}, nil
}

func (m *Manager) StageInput(ctx context.Context, ws Workspace, sourcePath, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return "", fmt.Errorf("stat input: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("input source must be a regular local file")
	}
	if info.Size() > m.maxInputBytes {
		return "", fmt.Errorf("input exceeds %d bytes", m.maxInputBytes)
	}
	name = safeFilename(name)
	if name == "" {
		return "", errors.New("input filename is required")
	}
	destination := filepath.Join(ws.InputDir, name)
	if !within(ws.InputDir, destination) {
		return "", errors.New("invalid input filename")
	}
	in, err := os.Open(sourcePath)
	if err != nil {
		return "", fmt.Errorf("open input: %w", err)
	}
	defer in.Close()
	return m.stageReader(in, destination)
}

// StageAttachment downloads or copies an inbound attachment into input/.
// Network access is limited to HTTP(S), bounded by maxInputBytes, and never
// writes directly to the final destination before the size check completes.
func (m *Manager) StageAttachment(ctx context.Context, ws Workspace, attachment mediadomain.MultimodalAttachment) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name := attachmentName(attachment)
	if name == "" {
		return "", errors.New("attachment filename is required")
	}
	destination := filepath.Join(ws.InputDir, name)
	if !within(ws.InputDir, destination) {
		return "", errors.New("invalid attachment filename")
	}
	if strings.TrimSpace(attachment.URL) != "" {
		target, err := url.Parse(attachment.URL)
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
			return "", errors.New("attachment URL must be an HTTP(S) URL")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return "", fmt.Errorf("create attachment request: %w", err)
		}
		response, err := m.httpClient.Do(request)
		if err != nil {
			return "", fmt.Errorf("download attachment: %w", err)
		}
		defer response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return "", fmt.Errorf("download attachment: HTTP %s", response.Status)
		}
		if response.ContentLength > m.maxInputBytes {
			return "", fmt.Errorf("attachment exceeds %d bytes", m.maxInputBytes)
		}
		return m.stageReader(response.Body, destination)
	}
	if strings.TrimSpace(attachment.ObjectKey) == "" {
		return "", errors.New("attachment has neither URL nor local file")
	}
	return m.StageInput(ctx, ws, attachment.ObjectKey, name)
}

func (m *Manager) stageReader(in io.Reader, destination string) (string, error) {
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create staged input: %w", err)
	}
	_, copyErr := io.Copy(out, io.LimitReader(in, m.maxInputBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return "", fmt.Errorf("stage input: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return "", fmt.Errorf("close staged input: %w", closeErr)
	}
	stagedInfo, err := os.Stat(destination)
	if err != nil {
		return "", fmt.Errorf("stat staged input: %w", err)
	}
	if stagedInfo.Size() > m.maxInputBytes {
		_ = os.Remove(destination)
		return "", fmt.Errorf("input exceeds %d bytes", m.maxInputBytes)
	}
	return destination, nil
}

func attachmentName(attachment mediadomain.MultimodalAttachment) string {
	for _, candidate := range []string{attachment.ObjectKey, urlPathBase(attachment.URL)} {
		if name := safeFilename(filepath.Base(candidate)); name != "" {
			return name
		}
	}
	ext := ""
	if attachment.MIME != "" {
		if extensions, _ := mime.ExtensionsByType(attachment.MIME); len(extensions) > 0 {
			ext = extensions[0]
		}
	}
	if id := safeFilename(attachment.AttachmentID); id != "" {
		return id + ext
	}
	return ""
}

func urlPathBase(raw string) string {
	target, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return path.Base(target.Path)
}

func (m *Manager) CollectOutputs(ctx context.Context, ws Workspace) ([]Artifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var artifacts []Artifact
	var total int64
	err := filepath.WalkDir(ws.OutputDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if len(artifacts) >= m.maxOutputFiles {
			return fmt.Errorf("output exceeds %d files", m.maxOutputFiles)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("output is not a regular file: %s", path)
		}
		if info.Size() > m.maxOutputBytes || total > m.maxOutputBytes-info.Size() {
			return fmt.Errorf("output exceeds %d bytes", m.maxOutputBytes)
		}
		hash, err := fileSHA256(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(ws.OutputDir, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("output path escapes workspace: %s", path)
		}
		artifacts = append(artifacts, Artifact{
			Name:      filepath.ToSlash(relative),
			Path:      path,
			Size:      info.Size(),
			SHA256:    hash,
			MediaType: mime.TypeByExtension(filepath.Ext(path)),
		})
		total += info.Size()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return artifacts, nil
}

// FileSegment turns a previously collected output into a platform-neutral
// file message segment. It revalidates the path so callers cannot smuggle an
// arbitrary host path into the outbound adapter.
func (m *Manager) FileSegment(ws Workspace, artifact Artifact) (conversationdomain.MessageSegment, error) {
	if artifact.Path == "" || !within(ws.OutputDir, artifact.Path) {
		return conversationdomain.MessageSegment{}, errors.New("artifact is outside workspace output")
	}
	info, err := os.Lstat(artifact.Path)
	if err != nil {
		return conversationdomain.MessageSegment{}, fmt.Errorf("stat artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return conversationdomain.MessageSegment{}, errors.New("artifact must be a regular file")
	}
	name := safeFilename(artifact.Name)
	if name == "" {
		name = filepath.Base(artifact.Path)
	}
	return conversationdomain.MessageSegment{
		Type: "file",
		Data: map[string]any{
			"file": artifact.Path,
			"name": name,
		},
	}, nil
}

func (m *Manager) Cleanup(ws Workspace) error {
	if ws.Root == "" || !within(m.root, ws.Root) || filepath.Clean(ws.Root) == filepath.Clean(m.root) {
		return errors.New("invalid workspace cleanup path")
	}
	return os.RemoveAll(ws.Root)
}

func within(root, path string) bool {
	root, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func safeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return ""
	}
	return name
}

func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return ""
	}
	return name
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open output: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash output: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
