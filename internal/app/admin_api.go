package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *App) adminAPI() *http.ServeMux {
	api := http.NewServeMux()
	api.HandleFunc("GET /admin/api/groups", func(w http.ResponseWriter, r *http.Request) {
		groups, err := a.store.Groups(r.Context())
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, groups)
	})
	api.HandleFunc("GET /admin/api/conversation", func(w http.ResponseWriter, r *http.Request) {
		groupID, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
		if err != nil || groupID <= 0 {
			writeJSON(w, 400, map[string]string{"error": "请选择群"})
			return
		}
		events, err := a.store.Recent(r.Context(), groupID, 100)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		notes, err := a.store.Notes(r.Context(), groupID, 200)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		attempts, err := a.store.Attempts(r.Context(), groupID)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"messages": events, "notes": notes, "attempts": attempts})
	})
	api.HandleFunc("PATCH /admin/api/notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Group   int64  `json:"group"`
			Version int64  `json:"version"`
			Text    string `json:"text"`
			Forget  bool   `json:"forget"`
		}
		if !requestJSON(w, r, &body) {
			return
		}
		if err := a.store.EditNote(r.Context(), body.Group, r.PathValue("id"), body.Version, body.Text, body.Forget); err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	api.HandleFunc("GET /admin/api/stickers", func(w http.ResponseWriter, r *http.Request) {
		assets, err := a.store.AdminStickers(r.Context())
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		out := []map[string]any{}
		for _, sticker := range assets {
			out = append(out, map[string]any{"id": sticker.ID, "description": sticker.Description, "tags": sticker.Tags, "state": sticker.State, "version": sticker.Version})
		}
		writeJSON(w, 200, out)
	})
	api.HandleFunc("PATCH /admin/api/stickers/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Version     int64  `json:"version"`
			Description string `json:"description"`
			Blocked     bool   `json:"blocked"`
		}
		if !requestJSON(w, r, &body) {
			return
		}
		if err := a.store.EditSticker(r.Context(), r.PathValue("id"), body.Version, body.Description, body.Blocked); err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	api.HandleFunc("GET /admin/api/stickers/{id}/image", func(w http.ResponseWriter, r *http.Request) {
		asset, err := a.store.GetSticker(r.Context(), r.PathValue("id"))
		if err != nil || asset.Path == "" {
			http.NotFound(w, r)
			return
		}
		root, _ := filepath.Abs(a.cfg.Meme.Path)
		target, _ := filepath.Abs(asset.Path)
		rel, err := filepath.Rel(root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", asset.MIME)
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, target)
	})
	return api
}
