package app

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

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
