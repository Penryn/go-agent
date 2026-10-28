package app

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed admin-dist
var adminBuildFS embed.FS

var (
	adminWebFS     = mustSubFS(adminBuildFS, "admin-dist")
	adminAssetHTTP = http.StripPrefix("/admin/", http.FileServer(http.FS(adminWebFS)))
)

func mustSubFS(source fs.FS, dir string) fs.FS {
	root, err := fs.Sub(source, dir)
	if err != nil {
		panic(err)
	}
	return root
}

const adminCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func serveAdminIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	page, err := fs.ReadFile(adminWebFS, "index.html")
	if err != nil {
		http.Error(w, "admin page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", adminCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(page))
}

func serveAdminAsset(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/admin/assets/" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	adminAssetHTTP.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func requestJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求格式不正确"})
		return false
	}
	return true
}
func (a *App) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if a.cfg.Server.AdminToken != "" {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(a.cfg.Server.AdminToken)) != 1 {
				writeJSON(w, 401, map[string]string{"error": "需要后台访问令牌"})
				return
			}
		} else {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			ip, parseErr := netip.ParseAddr(host)
			if err != nil || parseErr != nil || !ip.IsLoopback() {
				writeJSON(w, 403, map[string]string{"error": "未设置令牌时仅允许本机访问"})
				return
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					writeJSON(w, 403, map[string]string{"error": "请求来源不匹配"})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		err := a.store.DB.PingContext(ctx)
		if err == nil && a.cfg.QQ.Enabled {
			err = a.store.CheckSender(ctx)
			if err == nil && !a.qq.Connected.Load() {
				err = errors.New("QQ disconnected")
			}
		}
		status := 200
		if err != nil {
			status = 503
		}
		writeJSON(w, status, map[string]any{"ok": err == nil, "qq_enabled": a.cfg.QQ.Enabled, "qq_connected": a.qq.Connected.Load(), "main_model_configured": a.cfg.Models.Main.Model != "" && a.cfg.Models.Main.APIKey != "", "embedding_configured": a.cfg.Models.Embedding.Model != "" && a.cfg.Models.Embedding.APIKey != ""})
	})
	mux.HandleFunc("/admin/assets", http.NotFound)
	mux.HandleFunc("/admin/assets/", serveAdminAsset)
	mux.HandleFunc("/admin/", serveAdminIndex)
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
		g, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
		if err != nil || g <= 0 {
			writeJSON(w, 400, map[string]string{"error": "请选择群"})
			return
		}
		events, err := a.store.Recent(r.Context(), g, 100)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		notes, err := a.store.Notes(r.Context(), g, 200)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		attempts, err := a.store.Attempts(r.Context(), g)
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
		for _, s := range assets {
			out = append(out, map[string]any{"id": s.ID, "description": s.Description, "tags": s.Tags, "state": s.State, "version": s.Version})
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
	mux.Handle("/admin/api/", a.authorize(api))
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
	})
	return mux
}
