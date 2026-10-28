package app

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/phlin/go-agent/internal/config"
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

func TestAdminReactStaticRoutes(t *testing.T) {
	a := &App{cfg: config.Default()}
	handler := a.routes()
	serve := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, nil)
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	t.Run("root redirect", func(t *testing.T) {
		w := serve(http.MethodGet, "/admin")
		if w.Code != http.StatusTemporaryRedirect {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusTemporaryRedirect)
		}
		if got := w.Header().Get("Location"); got != "/admin/" {
			t.Fatalf("location = %q, want /admin/", got)
		}
	})

	index := serve(http.MethodGet, "/admin/")
	if index.Code != http.StatusOK {
		t.Fatalf("GET /admin/ status = %d", index.Code)
	}
	if got := index.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("content type = %q", got)
	}
	if got := index.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q", got)
	}
	csp := index.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "style-src 'self'") {
		t.Fatalf("CSP does not restrict scripts and styles to self: %q", csp)
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP still permits inline content: %q", csp)
	}
	if got := index.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("frame options = %q", got)
	}
	if !strings.Contains(index.Body.String(), `<div id="root"></div>`) {
		t.Fatal("index does not contain the React root")
	}

	head := serve(http.MethodHead, "/admin/")
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD /admin/ status = %d", head.Code)
	}
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD /admin/ returned %d body bytes", head.Body.Len())
	}

	assetPattern := regexp.MustCompile(`(?:src|href)="(/admin/assets/[^"]+\.(?:js|css))"`)
	matches := assetPattern.FindAllStringSubmatch(index.Body.String(), -1)
	if len(matches) != 2 {
		t.Fatalf("asset references = %d, want 2; index: %s", len(matches), index.Body.String())
	}
	for _, match := range matches {
		path := match[1]
		w := serve(http.MethodGet, path)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, w.Code)
		}
		if w.Body.Len() == 0 {
			t.Fatalf("GET %s returned an empty asset", path)
		}
		if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Fatalf("GET %s cache control = %q", path, got)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("GET %s content type options = %q", path, got)
		}
	}

	for _, path := range []string{
		"/admin/index.html",
		"/admin/missing",
		"/admin/assets",
		"/admin/assets/",
		"/admin/assets/missing.js",
	} {
		w := serve(http.MethodGet, path)
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want %d", path, w.Code, http.StatusNotFound)
		}
	}

	if w := serve(http.MethodPost, "/admin/"); w.Code != http.StatusNotFound {
		t.Fatalf("POST /admin/ status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAdminAPIRouteStillRequiresToken(t *testing.T) {
	a := &App{cfg: config.Default()}
	a.cfg.Server.AdminToken = "test-only-token"
	r := httptest.NewRequest(http.MethodGet, "http://localhost/admin/api/groups", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}
