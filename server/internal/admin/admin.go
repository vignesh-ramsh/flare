// Package admin serves the Admin UI: a small web page, built into the server's binary, where an
// administrator adds colour themes, changes the server's settings and manages users and access.
//
// It is plain HTML, CSS and JavaScript modules, with no build step and nothing fetched from
// elsewhere, so it works on a server with no internet, and a strict content security policy can
// forbid everything else. It does nothing the API does not: every change goes through the
// /v1/admin endpoints, which check that the caller is an administrator.
package admin

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed web
var embedded embed.FS

// Prefix is where the UI is served.
const Prefix = "/admin/"

// csp lets the page load its own scripts and styles and talk to its own server, and nothing
// else: no inline script or style, no other origin, no framing, no forms. A page that holds an
// access token gets no more freedom than it needs.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

type file struct {
	name string
	body []byte
	etag string
	mod  time.Time
}

// Files lists the files of the UI, by their path under Prefix.
func Files() []string {
	var out []string
	_ = fs.WalkDir(embedded, "web", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, strings.TrimPrefix(p, "web/"))
		}
		return nil
	})
	return out
}

// Content returns the bytes of one file of the UI, for tests.
func Content(name string) ([]byte, error) { return embedded.ReadFile("web/" + name) }

// Register adds the UI to mux: GET /admin goes to /admin/, which is the page, and every other
// file of the UI is served at its own exact path.
//
// Each file has a route of its own rather than one pattern matching everything under /admin/,
// so a topic that happens to be called "admin" keeps working: /admin/json and /admin/sse stay
// what they have always been.
func Register(mux *http.ServeMux) error {
	started := time.Now()
	for _, name := range Files() {
		body, err := embedded.ReadFile("web/" + name)
		if err != nil {
			return fmt.Errorf("admin ui: %w", err)
		}
		sum := sha256.Sum256(body)
		f := file{name: name, body: body, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, mod: started}
		if name == "index.html" {
			mux.HandleFunc("GET "+Prefix+"{$}", f.serve)
			continue
		}
		mux.HandleFunc("GET "+Prefix+name, f.serve)
	}
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, Prefix, http.StatusMovedPermanently)
	})
	return nil
}

func (f file) serve(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	if t := mime.TypeByExtension(path.Ext(f.name)); t != "" {
		h.Set("Content-Type", t)
	}
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	// The page is small and is the thing an administrator trusts: let it be checked every time,
	// cheaply, rather than ever being served stale after an upgrade.
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", f.etag)
	http.ServeContent(w, r, f.name, f.mod, bytes.NewReader(f.body))
}
