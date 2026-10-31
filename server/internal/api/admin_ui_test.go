package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestAdminUIIsServedByTheServer(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do("GET", "/admin/", "")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /admin/: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(data), `id="app"`) {
		t.Fatalf("not the Admin UI: %.200s", data)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("no content security policy: %v", resp.Header)
	}
	// The API's own header still applies.
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing nosniff: %v", resp.Header)
	}
	for _, name := range []string{"admin.js", "admin.css", "api.js", "palette.js", "ui.js", "views/themes.js"} {
		resp, _ := e.do("GET", "/admin/"+name, "")
		if resp.StatusCode != 200 {
			t.Errorf("GET /admin/%s: %d", name, resp.StatusCode)
		}
	}
}

func TestAdminWithoutSlashGoesToTheUI(t *testing.T) {
	e := newEnv(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(e.srv.URL + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/admin/" {
		t.Fatalf("got %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// "admin" is a perfectly good topic name, and was one before there was an Admin UI: it must
// still be possible to publish to it and to subscribe in every way.
func TestATopicCalledAdminStillWorks(t *testing.T) {
	e := newEnv(t)
	m := e.published("admin", "hello")
	if got := e.poll("/admin/json?poll=1"); len(got) != 1 || got[0].ID != m.ID {
		t.Fatalf("polling the topic: %+v", got)
	}
	resp, body, _ := openStream(t, e.srv.URL+"/admin/sse?since=all")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("subscribing with SSE: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var seen strings.Builder
	for i := 0; i < 8 && !strings.Contains(seen.String(), m.ID); i++ {
		line, err := body.ReadString('\n')
		if err != nil {
			t.Fatalf("reading the SSE stream (%q so far): %v", seen.String(), err)
		}
		seen.WriteString(line)
	}
	if !strings.Contains(seen.String(), m.ID) {
		t.Fatalf("the SSE stream never carried the message: %q", seen.String())
	}
	// And the UI is still there.
	if resp, _ := e.do("GET", "/admin/", ""); resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("the UI: %d", resp.StatusCode)
	}
}

// The files of the UI are public, like any web page, and carry no secrets; what an administrator
// can do is decided by the API, which they need a token for.
func TestAdminUIGivesNoPowerWithoutAToken(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/v1/admin/users", "/v1/admin/settings", "/v1/admin/access"} {
		resp, data := e.do("GET", path, "")
		if resp.StatusCode != 401 && resp.StatusCode != 403 {
			t.Errorf("GET %s without a token: %d %s", path, resp.StatusCode, data)
		}
	}
}
