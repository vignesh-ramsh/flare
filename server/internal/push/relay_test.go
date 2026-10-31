package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

type fakeRelay struct {
	*httptest.Server
	mu      sync.Mutex
	status  int
	headers map[string]string
	bodies  []string
	paths   []string
	ctypes  []string
}

func newFakeRelay(t *testing.T) *fakeRelay {
	r := &fakeRelay{status: 200}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies, r.paths, r.ctypes = append(r.bodies, string(body)), append(r.paths, req.Method+" "+req.URL.Path), append(r.ctypes, req.Header.Get("Content-Type"))
		status, headers := r.status, r.headers
		r.mu.Unlock()
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *fakeRelay) reply(status int, headers map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status, r.headers = status, headers
}

func (r *fakeRelay) last() (path, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paths[len(r.paths)-1], r.bodies[len(r.bodies)-1]
}

func relayNotification(relayURL string) Notification {
	now := time.Unix(1_800_000_000, 0)
	return Notification{
		Device:         Device{ID: "d1", Provider: Relay, RelayURL: relayURL, Target: "handle-0123456789abcdef"},
		SubscriptionID: "sub-1",
		// A message with everything a relay must never see.
		Message: message.Message{
			ID: "msg-1", Topic: "payroll", Title: "Confidential title", Message: "secret salaries", Priority: 4,
			Tags: []string{"private"}, Click: "https://example.com/private",
		},
		Payload: WakePayload("sub-1", "msg-1"), WakeUp: true, Now: now, Expires: now.Add(3 * time.Hour),
	}
}

func TestAWakeRequestSaysOnlyWhichSubscriptionAndMessage(t *testing.T) {
	relay := newFakeRelay(t)
	s := NewRelaySender(relay.URL, http.DefaultClient)
	if err := s.Send(context.Background(), relayNotification(relay.URL)); err != nil {
		t.Fatal(err)
	}
	path, body := relay.last()
	if path != "POST /v1/wake" || relay.ctypes[0] != "application/json" {
		t.Errorf("%s (%s)", path, relay.ctypes[0])
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("not JSON: %q", body)
	}
	want := map[string]any{"handle": "handle-0123456789abcdef", "sub": "sub-1", "id": "msg-1", "priority": float64(4), "ttl": float64(10800)}
	if len(got) != len(want) {
		t.Errorf("body = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	for _, leak := range []string{"payroll", "Confidential", "secret", "private", "example.com"} {
		if strings.Contains(body, leak) {
			t.Errorf("the relay was told %q: %s", leak, body)
		}
	}
}

func TestEverythingThroughARelayIsAPollRequest(t *testing.T) {
	s := NewRelaySender("https://relay.example.org", http.DefaultClient)
	m := message.Message{ID: "m", Topic: "t", Message: "a", Priority: 3}
	payload, wakeUp := PayloadFor(s, m, "sub", false)
	if !wakeUp || strings.Contains(string(payload), "\"a\"") {
		t.Errorf("a message for a relay was prepared as %s (wakeUp=%v): content must never go through one", payload, wakeUp)
	}
}

func TestAWakeRequestIsCappedAtFourWeeks(t *testing.T) {
	relay := newFakeRelay(t)
	s := NewRelaySender(relay.URL, http.DefaultClient)
	n := relayNotification(relay.URL)
	n.Expires = n.Now.Add(90 * 24 * time.Hour)
	if err := s.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	_, body := relay.last()
	if !strings.Contains(body, `"ttl":2419200`) {
		t.Errorf("body = %s", body)
	}
}

func TestARelaysAnswersAreSortedOut(t *testing.T) {
	relay := newFakeRelay(t)
	s := NewRelaySender(relay.URL, http.DefaultClient)
	tests := []struct {
		status     int
		headers    map[string]string
		kind       string
		retryAfter time.Duration
	}{
		{200, nil, "ok", 0},
		{204, nil, "ok", 0},
		{404, nil, "gone", 0}, // the handle is unknown: the device must register again
		{410, nil, "gone", 0},
		{429, map[string]string{"Retry-After": "45"}, "transient", 45 * time.Second},
		{503, map[string]string{"Retry-After": "10"}, "transient", 10 * time.Second},
		{502, nil, "transient", 0},
		{400, nil, "permanent", 0},
		{422, nil, "permanent", 0},
	}
	for _, tt := range tests {
		relay.reply(tt.status, tt.headers)
		err := s.Send(context.Background(), relayNotification(relay.URL))
		got := "ok"
		var se *SendError
		if errors.As(err, &se) {
			switch {
			case se.Unregistered:
				got = "gone"
			case se.Permanent:
				got = "permanent"
			default:
				got = "transient"
			}
		} else if err != nil {
			got = "other: " + err.Error()
		}
		if got != tt.kind || (se != nil && se.RetryAfter != tt.retryAfter) {
			t.Errorf("%d: %s (%v), want %s retryAfter=%v", tt.status, got, err, tt.kind, tt.retryAfter)
		}
	}
}

func TestNormalizeRelayURL(t *testing.T) {
	good := map[string]string{
		"https://push.example.org":          "https://push.example.org",
		"https://push.example.org/":         "https://push.example.org",
		" https://push.example.org/relay/ ": "https://push.example.org/relay",
		"https://push.example.org:8443":     "https://push.example.org:8443",
		"http://127.0.0.1:9000":             "http://127.0.0.1:9000", // for development
		"http://localhost:9000/":            "http://localhost:9000",
		"http://[::1]:9000":                 "http://[::1]:9000",
	}
	for in, want := range good {
		if got, err := NormalizeRelayURL(in); err != nil || got != want {
			t.Errorf("NormalizeRelayURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "push.example.org", "ftp://push.example.org", "https://", "http://push.example.org",
		"http://192.168.1.5:9000", "https://user:pass@push.example.org", "https://push.example.org?x=1", "https://push.example.org#f",
	} {
		if got, err := NormalizeRelayURL(in); err == nil {
			t.Errorf("NormalizeRelayURL(%q) = %q, want an error", in, got)
		}
	}
}

func TestValidHandle(t *testing.T) {
	for _, h := range []string{strings.Repeat("a", 16), "Abc_-123Abc_-123Abc_-123", strings.Repeat("z", 128)} {
		if !ValidHandle(h) {
			t.Errorf("%q should be a handle", h)
		}
	}
	for _, h := range []string{"", "short", strings.Repeat("a", 15), strings.Repeat("a", 129), strings.Repeat("a", 16) + "/", strings.Repeat("a", 15) + " ", strings.Repeat("é", 20)} {
		if ValidHandle(h) {
			t.Errorf("%q should not be a handle", h)
		}
	}
}
