package app

import (
	"context"
	"errors"
	"net/http"
	"time"
)

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
	mux.Handle("/admin/api/", a.authorize(a.adminAPI()))
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
	})
	return mux
}
