package app

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"net/http"
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

const (
	adminCSPTemplate         = "default-src 'self'; script-src 'self'; style-src 'self' 'nonce-%s'; img-src 'self' blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	adminCSPNoncePlaceholder = "__ADMIN_CSP_NONCE__"
)

func newAdminCSPNonce() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(value[:]), nil
}

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
	nonce, err := newAdminCSPNonce()
	if err != nil {
		http.Error(w, "admin page unavailable", http.StatusInternalServerError)
		return
	}
	page = bytes.Replace(page, []byte(adminCSPNoncePlaceholder), []byte(nonce), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", fmt.Sprintf(adminCSPTemplate, nonce))
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
