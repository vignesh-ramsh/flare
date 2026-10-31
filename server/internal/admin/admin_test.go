package admin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"
)

func newMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	if err := Register(mux); err != nil {
		t.Fatal(err)
	}
	return mux
}

func get(mux http.Handler, target string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestEveryFileIsServedWithTheRightTypeAndPolicy(t *testing.T) {
	mux := newMux(t)
	types := map[string]string{".html": "text/html", ".css": "text/css", ".js": "javascript"}
	files := Files()
	if len(files) < 10 {
		t.Fatalf("only %d files are embedded: %v", len(files), files)
	}
	for _, name := range files {
		target := Prefix + name
		if name == "index.html" {
			target = Prefix
		}
		rec := get(mux, target)
		if rec.Code != 200 {
			t.Errorf("%s: status %d", target, rec.Code)
			continue
		}
		want, ok := types[path.Ext(name)]
		if !ok {
			t.Errorf("%s: no expected type for this extension; teach the test", name)
		}
		if got := rec.Header().Get("Content-Type"); !strings.Contains(got, want) {
			t.Errorf("%s: Content-Type %q, want it to contain %q", target, got, want)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != csp {
			t.Errorf("%s: Content-Security-Policy %q", target, got)
		}
		for header, value := range map[string]string{"X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer", "Cache-Control": "no-cache"} {
			if got := rec.Header().Get(header); got != value {
				t.Errorf("%s: %s is %q, want %q", target, header, got, value)
			}
		}
		want2, _ := Content(name)
		if rec.Body.String() != string(want2) {
			t.Errorf("%s: body differs from the embedded file", target)
		}
		// Cheap to check again: a browser sends the ETag back.
		again := get(mux, target, "If-None-Match", rec.Header().Get("ETag"))
		if again.Code != http.StatusNotModified {
			t.Errorf("%s: with its ETag, status %d, want 304", target, again.Code)
		}
	}
}

func TestThePolicyForbidsEverythingThePageDoesNotNeed(t *testing.T) {
	for _, must := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, must) {
			t.Errorf("the policy lacks %q: %s", must, csp)
		}
	}
	for _, never := range []string{"unsafe-inline", "unsafe-eval", "*", "http:", "https:"} {
		if strings.Contains(csp, never) {
			t.Errorf("the policy must not contain %q: %s", never, csp)
		}
	}
}

func TestAdminWithoutASlashRedirects(t *testing.T) {
	rec := get(newMux(t), "/admin")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/admin/" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestOnlyGetIsAllowed(t *testing.T) {
	mux := newMux(t)
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(method, Prefix, strings.NewReader("x"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", method, rec.Code)
		}
	}
}

func TestOnlyEmbeddedFilesAreServed(t *testing.T) {
	mux := newMux(t)
	for _, target := range []string{"/admin/nope.js", "/admin/views/", "/admin/views", "/admin/web/index.html", "/admin/%2e%2e/etc/passwd", "/admin/../../etc/passwd", "/admin/views/../../admin.go"} {
		rec := get(mux, target)
		if rec.Code == 200 {
			body, _ := io.ReadAll(rec.Body)
			t.Errorf("%s: status 200 with %d bytes; only the listed files may be served", target, len(body))
		}
	}
}

// Nothing in the page may run code the policy would forbid, or build markup from text. The
// policy is the lock; these checks keep the code from needing it opened.
func TestPageHasNoInlineCodeAndBuildsNoMarkupFromText(t *testing.T) {
	scriptTag := regexp.MustCompile(`(?is)<script\b[^>]*>`)
	handler := regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	styleAttr := regexp.MustCompile(`(?i)\sstyle\s*=`)
	styleTag := regexp.MustCompile(`(?i)<style\b`)
	for _, name := range Files() {
		body, err := Content(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		switch path.Ext(name) {
		case ".html":
			for _, tag := range scriptTag.FindAllString(text, -1) {
				if !strings.Contains(tag, "src=") {
					t.Errorf("%s has an inline script: %s", name, tag)
				}
			}
			for label, re := range map[string]*regexp.Regexp{"an event handler attribute": handler, "a style attribute": styleAttr, "a style element": styleTag} {
				if re.MatchString(text) {
					t.Errorf("%s has %s", name, label)
				}
			}
		case ".js":
			for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "setAttribute('style'", `setAttribute("style"`, "srcdoc", "createContextualFragment", "DOMParser"} {
				if strings.Contains(text, bad) {
					t.Errorf("%s uses %s", name, bad)
				}
			}
			if regexp.MustCompile(`\bfetch\(\s*['"\x60]https?:`).MatchString(text) {
				t.Errorf("%s fetches from another origin", name)
			}
		}
	}
}

func TestEveryReferencedFileExists(t *testing.T) {
	have := map[string]bool{}
	for _, name := range Files() {
		have[name] = true
	}
	html, _ := Content("index.html")
	for _, m := range regexp.MustCompile(`(?:href|src)="(/admin/[^"]*)"`).FindAllStringSubmatch(string(html), -1) {
		name := strings.TrimPrefix(m[1], Prefix)
		if !have[name] {
			t.Errorf("index.html refers to %s, which is not embedded", m[1])
		}
	}
	imports := regexp.MustCompile(`from '(\.{1,2}/[^']+)'`)
	for _, name := range Files() {
		if path.Ext(name) != ".js" {
			continue
		}
		body, _ := Content(name)
		for _, m := range imports.FindAllStringSubmatch(string(body), -1) {
			target := path.Join(path.Dir(name), m[1])
			if !have[target] {
				t.Errorf("%s imports %s, which is not embedded", name, m[1])
			}
		}
	}
}

func TestEveryScriptIsImportedOrLoaded(t *testing.T) {
	// A file nobody refers to is dead weight in the binary, and probably a forgotten rename.
	used := map[string]bool{"index.html": true}
	html, _ := Content("index.html")
	for _, m := range regexp.MustCompile(`(?:href|src)="/admin/([^"]*)"`).FindAllStringSubmatch(string(html), -1) {
		used[m[1]] = true
	}
	imports := regexp.MustCompile(`from '(\.{1,2}/[^']+)'`)
	for range Files() { // imports can chain, so settle in as many passes as there are files
		for _, name := range Files() {
			if !used[name] || path.Ext(name) != ".js" {
				continue
			}
			body, _ := Content(name)
			for _, m := range imports.FindAllStringSubmatch(string(body), -1) {
				used[path.Join(path.Dir(name), m[1])] = true
			}
		}
	}
	for _, name := range Files() {
		if !used[name] {
			t.Errorf("%s is embedded but nothing loads it", name)
		}
	}
}
