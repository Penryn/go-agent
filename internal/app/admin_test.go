package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phlin/go-agent/internal/testsupport"
)

func TestSummarizeTaskPayload(t *testing.T) {
	got := summarizeTaskPayload([]byte(`{"text":"hidden","group_id":42,"z":true,"a":1}`))
	if got.GroupID != 42 || len(got.PayloadKeys) != 4 || got.PayloadKeys[0] != "a" || got.PayloadKeys[3] != "z" {
		t.Fatalf("unexpected task context: %+v", got)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatal(err)
	}
	if invalid := summarizeTaskPayload([]byte(`not-json`)); len(invalid.PayloadKeys) != 0 || invalid.GroupID != 0 {
		t.Fatalf("unexpected invalid payload summary: %+v", invalid)
	}
}

func TestAdminHandlerServesVueAppAndProtectsData(t *testing.T) {
	assets, err := fs.Sub(adminAssets, "adminui/dist")
	if err != nil {
		t.Fatal(err)
	}
	handler := &adminHandler{
		token: "secret",
		load: func(context.Context, int64) (adminSnapshot, error) {
			return adminSnapshot{SelectedGroup: 42}, nil
		},
		assets: http.StripPrefix("/admin/", http.FileServer(http.FS(assets))),
	}

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), `<div id="app"></div>`) {
		t.Fatalf("unexpected admin index: status=%d body=%q", index.Code, index.Body.String())
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/admin/api/snapshot", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", unauthorized.Code)
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, "/admin/api/snapshot?group_id=42", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer secret")
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK || !strings.Contains(authorized.Body.String(), `"selected_group":42`) {
		t.Fatalf("unexpected authorized response: status=%d body=%q", authorized.Code, authorized.Body.String())
	}
}

func TestAdminWithoutTokenIsLoopbackOnly(t *testing.T) {
	handler := &adminHandler{load: func(context.Context, int64) (adminSnapshot, error) { return adminSnapshot{}, nil }}

	local := httptest.NewRequest(http.MethodGet, "/admin/api/snapshot", nil)
	local.RemoteAddr = "127.0.0.1:1234"
	localResponse := httptest.NewRecorder()
	handler.ServeHTTP(localResponse, local)
	if localResponse.Code != http.StatusOK {
		t.Fatalf("expected loopback access, got %d", localResponse.Code)
	}

	remote := httptest.NewRequest(http.MethodGet, "/admin/api/snapshot", nil)
	remote.RemoteAddr = "192.0.2.1:1234"
	remoteResponse := httptest.NewRecorder()
	handler.ServeHTTP(remoteResponse, remote)
	if remoteResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected remote access to be denied, got %d", remoteResponse.Code)
	}
}

func TestAdminMemeFileServesLocalFileAndRejectsTraversal(t *testing.T) {
	storage := t.TempDir()
	if err := os.WriteFile(filepath.Join(storage, "ok.png"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := &adminHandler{memeStoragePath: storage}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/memes/files/ok.png", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "image" {
		t.Fatalf("unexpected meme file response: status=%d body=%q", response.Code, response.Body.String())
	}
	traversal := httptest.NewRequest(http.MethodGet, "/admin/api/memes/files/%2e%2e%2fsecret", nil)
	traversal.RemoteAddr = "127.0.0.1:1234"
	traversalResponse := httptest.NewRecorder()
	handler.ServeHTTP(traversalResponse, traversal)
	if traversalResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected traversal rejection, got %d", traversalResponse.Code)
	}
}

func TestAdminSnapshotCoreMode(t *testing.T) {
	handler := &adminHandler{
		token:      "secret",
		load:       func(context.Context, int64) (adminSnapshot, error) { return adminSnapshot{SelectedGroup: 1}, nil },
		loadWindow: func(context.Context, int64, int) (adminSnapshot, error) { return adminSnapshot{SelectedGroup: 2}, nil },
		loadCore:   func(context.Context, int64, int) (adminSnapshot, error) { return adminSnapshot{SelectedGroup: 3}, nil },
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/snapshot?mode=core", nil)
	req.Header.Set("Authorization", "Bearer secret")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"selected_group":3`) {
		t.Fatalf("unexpected core snapshot: status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAdminSnapshotNormalizesEmptyCollections(t *testing.T) {
	handler := &adminHandler{
		load: func(context.Context, int64) (adminSnapshot, error) {
			return adminSnapshot{Persona: adminPersona{}}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/snapshot", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	body := response.Body.String()
	for _, field := range []string{`"groups":[]`, `"memories":[]`, `"relationships":[]`, `"activity":[]`, `"facts":[]`, `"interests":[]`} {
		if !strings.Contains(body, field) {
			t.Fatalf("snapshot missing empty array %s: %s", field, body)
		}
	}
}

func TestAdminRetrievalMetricsAcceptIntegerWindow(t *testing.T) {
	db := testsupport.NewDB(t)
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO retrieval_traces (
			trace_id, event_id, group_id, user_id, query, candidate_count,
			hit_memory_ids_json, selected_memory_ids_json, outcome, created_at
		) VALUES ($1, $2, 1, 2, 'test', 1, '[]'::jsonb, '[]'::jsonb, '', NOW())
	`, "trace-admin-window", "event-admin-window")
	if err != nil {
		t.Fatal(err)
	}

	metrics, err := (&adminDashboard{db: db}).loadRetrievalMetrics(context.Background(), 0, 1440)
	if err != nil {
		t.Fatalf("load retrieval metrics: %v", err)
	}
	if metrics.Queries != 1 {
		t.Fatalf("expected one retrieval query, got %+v", metrics)
	}
}
