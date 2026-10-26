package app

import (
	"github.com/phlin/go-agent/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminAuthDoesNotTrustForwardedAddress(t *testing.T) {
	a := &App{cfg: config.Default()}
	handler := a.authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		remote string
		want   int
	}{{"127.0.0.1:123", 204}, {"[::1]:123", 204}, {"192.168.0.5:123", 403}, {"invalid", 403}} {
		r := httptest.NewRequest("GET", "http://localhost/admin/api/groups", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code)
		}
	}
}
func TestAdminTokenAndOrigin(t *testing.T) {
	a := &App{cfg: config.Default()}
	a.cfg.Server.AdminToken = "test-only-token"
	handler := a.authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		token, origin string
		want          int
	}{{"", "", 401}, {"Bearer test-only-token", "https://unrelated.invalid", 403}, {"Bearer test-only-token", "http://localhost", 204}} {
		r := httptest.NewRequest("PATCH", "http://localhost/admin/api/notes/a", strings.NewReader(`{}`))
		r.Header.Set("Authorization", tc.token)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code)
		}
	}
}
