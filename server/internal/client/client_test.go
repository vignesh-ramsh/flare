package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/id"
	"github.com/vignesh-ramsh/flare/server/internal/message"
)

var gen id.Generator
var genMu sync.Mutex

func newID() string {
	genMu.Lock()
	defer genMu.Unlock()
	return gen.New(time.Now())
}

func msg(body string) message.Message {
	return message.Message{ID: newID(), Time: time.Now().Unix(), Event: "message", Topic: "t", Message: body, Priority: 3}
}

func send(w http.ResponseWriter, events ...message.Message) {
	for _, e := range events {
		json.NewEncoder(w).Encode(e)
	}
	w.(http.Flusher).Flush()
}

func open(cp string) message.Message {
	return message.Message{ID: cp, Event: "open", Topic: "t", Time: time.Now().Unix()}
}

// script plays one scripted handler per request, recording each request's query.
// After the script runs out, further requests hang until the client leaves.
type script struct {
	mu       sync.Mutex
	handlers []func(w http.ResponseWriter, r *http.Request)
	queries  []string
	times    []time.Time
}

func (s *script) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	n := len(s.queries)
	s.queries = append(s.queries, r.URL.RawQuery)
	s.times = append(s.times, time.Now())
	var h func(http.ResponseWriter, *http.Request)
	if n < len(s.handlers) {
		h = s.handlers[n]
	}
	s.mu.Unlock()
	if h == nil {
		h = hang
	}
	h(w, r)
}

func hang(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	w.(http.Flusher).Flush()
	<-r.Context().Done()
}

func (s *script) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queries)
}

func (s *script) query(i int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queries[i]
}

func start(t *testing.T, handlers ...func(http.ResponseWriter, *http.Request)) (*Client, *script) {
	t.Helper()
	s := &script{handlers: handlers}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c.MinBackoff, c.MaxBackoff, c.IdleTimeout = 5*time.Millisecond, 20*time.Millisecond, 10*time.Second
	return c, s
}

// collect runs Subscribe in the background and gathers the messages it handles.
type collector struct {
	mu   sync.Mutex
	got  []string
	done chan error
	stop context.CancelFunc
}

func subscribe(t *testing.T, c *Client, opt SubscribeOptions) *collector {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	col := &collector{done: make(chan error, 1), stop: cancel}
	t.Cleanup(cancel)
	go func() {
		col.done <- c.Subscribe(ctx, []string{"t"}, opt, func(m message.Message) error {
			col.mu.Lock()
			col.got = append(col.got, m.Message)
			col.mu.Unlock()
			return nil
		})
	}()
	return col
}

func (c *collector) messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.got...)
}

func (c *collector) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.messages(); len(got) >= n {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out with %v, waiting for %d messages", c.messages(), n)
	return nil
}

func (c *collector) finish(t *testing.T) error {
	t.Helper()
	c.stop()
	select {
	case err := <-c.done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe did not return after its context was cancelled")
		return nil
	}
}

func TestNewValidatesTheAddress(t *testing.T) {
	for _, bad := range []string{"", "flare.example.com", "ftp://x.example", "https://", "://nope"} {
		if _, err := New(bad); err == nil {
			t.Errorf("New(%q) succeeded", bad)
		}
	}
	if c, err := New("https://flare.example.com/"); err != nil || c.base != "https://flare.example.com" {
		t.Errorf("New with a trailing slash = %+v, %v", c, err)
	}
}

func TestPublishSendsTheMessageAndItsSettings(t *testing.T) {
	var got *http.Request
	var body strings.Builder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		buf := make([]byte, 100)
		n, _ := r.Body.Read(buf)
		body.Write(buf[:n])
		json.NewEncoder(w).Encode(message.Message{ID: "x", Event: "message", Topic: "alerts", Message: "stored"})
	}))
	defer srv.Close()
	c, _ := New(srv.URL)

	m, err := c.Publish(context.Background(), "alerts", "disk is full",
		PublishOptions{Title: "Café", Priority: "high", Tags: []string{"warning", "disk"}, Click: "https://example.com"})
	if err != nil || m.Topic != "alerts" || m.Message != "stored" {
		t.Fatalf("Publish = %+v, %v", m, err)
	}
	if got.Method != "POST" || got.URL.Path != "/alerts" || body.String() != "disk is full" {
		t.Errorf("request = %s %s %q", got.Method, got.URL.Path, body.String())
	}
	if h := got.Header.Get("Title"); h != "=?utf-8?q?Caf=C3=A9?=" {
		t.Errorf("Title header = %q, want an RFC 2047 encoded word, which survives proxies", h)
	}
	if got.Header.Get("Priority") != "high" || got.Header.Get("Tags") != "warning,disk" || got.Header.Get("Click") != "https://example.com" {
		t.Errorf("headers = %v", got.Header)
	}

	// Settings that are not given are not sent at all.
	if _, err := c.Publish(context.Background(), "t", "x", PublishOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"Title", "Priority", "Tags", "Click"} {
		if got.Header.Get(h) != "" {
			t.Errorf("header %s was sent although not set", h)
		}
	}
}

func TestPublishReportsWhatTheServerSaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/denied":
			w.WriteHeader(403)
			w.Write([]byte(`{"code":40301,"http":403,"error":"forbidden: this server does not allow write access"}`))
		case "/proxy":
			w.WriteHeader(502)
			w.Write([]byte("<html>Bad gateway</html>"))
		default:
			w.Write([]byte("not json"))
		}
	}))
	c, _ := New(srv.URL)

	_, err := c.Publish(context.Background(), "denied", "x", PublishOptions{})
	var api *APIError
	if !errors.As(err, &api) || api.HTTP != 403 || api.Code != 40301 || !strings.Contains(err.Error(), "does not allow write access") {
		t.Errorf("err = %v, want the server's own message and code", err)
	}
	_, err = c.Publish(context.Background(), "proxy", "x", PublishOptions{})
	if !errors.As(err, &api) || api.HTTP != 502 || api.Code != 0 || strings.Contains(err.Error(), "html") {
		t.Errorf("err = %v, want just the status, not an HTML page", err)
	}
	if _, err = c.Publish(context.Background(), "garbled", "x", PublishOptions{}); err == nil {
		t.Error("an answer that isn't a message was accepted")
	}
	srv.Close()
	if _, err = c.Publish(context.Background(), "t", "x", PublishOptions{}); err == nil {
		t.Error("publishing to a server that is gone succeeded")
	}
}

func TestSubscribeDeliversMessagesAndIgnoresHousekeeping(t *testing.T) {
	a, b := msg("a"), msg("b")
	c, _ := start(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		send(w, open("cp"), a, message.Message{Event: "keepalive"}, b, message.Message{Event: "something-new"})
		<-r.Context().Done()
	})
	col := subscribe(t, c, SubscribeOptions{})
	if got := col.waitFor(t, 2); strings.Join(got, ",") != "a,b" {
		t.Errorf("got %v", got)
	}
	if err := col.finish(t); err != nil {
		t.Errorf("Subscribe returned %v after cancellation, want nil", err)
	}
}

func TestSubscribeReconnectsAndResumesAfterTheLastMessage(t *testing.T) {
	m1, m2 := msg("one"), msg("two")
	c, s := start(t,
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); send(w, open("cp"), m1) }, // then hangs up
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			send(w, open("cp2"), m2)
			<-r.Context().Done()
		},
	)
	var retries []error
	c.OnRetry = func(err error, wait time.Duration) { retries = append(retries, err) }
	col := subscribe(t, c, SubscribeOptions{})
	if got := col.waitFor(t, 2); strings.Join(got, ",") != "one,two" {
		t.Errorf("got %v", got)
	}
	col.finish(t)
	if q := s.query(1); q != "since="+m1.ID {
		t.Errorf("the second request asked for %q, want everything since the last message", q)
	}
	if len(retries) != 1 || !strings.Contains(retries[0].Error(), "closed the connection") {
		t.Errorf("retries = %v", retries)
	}
}

func TestLiveSubscriberResumesFromTheOpenEventUntilItHasAMessage(t *testing.T) {
	// Asked for new messages only, and nothing has arrived: a message published
	// while reconnecting must still be caught, so resume from the open event.
	cp := id.Boundary(time.Now())
	c, s := start(t,
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); send(w, open(cp)) },
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			send(w, open("later"))
			<-r.Context().Done()
		},
	)
	col := subscribe(t, c, SubscribeOptions{})
	for s.requests() < 2 {
		time.Sleep(2 * time.Millisecond)
	}
	col.finish(t)
	if q := s.query(0); q != "" {
		t.Errorf("first request = %q, want no since for a live subscriber", q)
	}
	if q := s.query(1); q != "since="+cp {
		t.Errorf("second request = %q, want it to resume from the open event's ID", q)
	}
}

func TestExplicitSinceIsNotReplacedByTheOpenEvent(t *testing.T) {
	// The backlog hasn't been delivered yet, so resuming from "now" would skip it.
	c, s := start(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			send(w, open(id.Boundary(time.Now())))
		},
	)
	col := subscribe(t, c, SubscribeOptions{Since: "all"})
	for s.requests() < 2 {
		time.Sleep(2 * time.Millisecond)
	}
	col.finish(t)
	if s.query(0) != "since=all" || s.query(1) != "since=all" {
		t.Errorf("requests = %q, %q; both should ask for everything", s.query(0), s.query(1))
	}
}

func TestRepeatedMessagesAreHandledOnce(t *testing.T) {
	m1, m2, m3 := msg("one"), msg("two"), msg("three")
	c, _ := start(t,
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); send(w, open("cp"), m1, m2) },
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			send(w, m2, m3, m1)
			<-r.Context().Done()
		},
	)
	col := subscribe(t, c, SubscribeOptions{})
	got := col.waitFor(t, 3)
	time.Sleep(30 * time.Millisecond)
	col.finish(t)
	if got = col.messages(); strings.Join(got, ",") != "one,two,three" {
		t.Errorf("got %v, want each message once, in order", got)
	}
}

func TestSubscribeGivesUpOnErrorsThatRetryingCannotFix(t *testing.T) {
	refuse := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"code":40301,"http":403,"error":"forbidden: no access"}`))
	}
	c, s := start(t, refuse, refuse, refuse)
	err := c.Subscribe(context.Background(), []string{"t"}, SubscribeOptions{}, func(message.Message) error { return nil })
	var api *APIError
	if !errors.As(err, &api) || api.HTTP != 403 {
		t.Errorf("err = %v, want the 403", err)
	}
	if s.requests() != 1 {
		t.Errorf("made %d requests, want 1: a refusal won't change", s.requests())
	}
}

func TestSubscribeKeepsTryingWhenTheServerIsUnavailable(t *testing.T) {
	m := msg("finally")
	unavailable := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }
	c, s := start(t, unavailable, unavailable,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			send(w, open("cp"), m)
			<-r.Context().Done()
		})
	col := subscribe(t, c, SubscribeOptions{})
	if got := col.waitFor(t, 1); got[0] != "finally" {
		t.Errorf("got %v", got)
	}
	col.finish(t)
	if s.requests() != 3 {
		t.Errorf("made %d requests, want 3", s.requests())
	}
}

func TestSubscribeKeepsTryingWhenTheServerIsNotThereAtAll(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, _ := New(url)
	c.MinBackoff, c.MaxBackoff = 5*time.Millisecond, 10*time.Millisecond
	attempts := 0
	c.OnRetry = func(error, time.Duration) { attempts++ }
	col := subscribe(t, c, SubscribeOptions{})
	time.Sleep(150 * time.Millisecond)
	if err := col.finish(t); err != nil {
		t.Errorf("Subscribe = %v, want nil: it was cancelled, not defeated", err)
	}
	if attempts < 3 {
		t.Errorf("only %d attempts in 150ms", attempts)
	}
}

func TestSubscribeStopsWhenTheHandlerFails(t *testing.T) {
	boom := errors.New("cannot show notification")
	c, s := start(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		send(w, open("cp"), msg("x"), msg("y"))
	})
	calls := 0
	err := c.Subscribe(context.Background(), []string{"t"}, SubscribeOptions{}, func(message.Message) error { calls++; return boom })
	if err != boom || calls != 1 || s.requests() != 1 {
		t.Errorf("err = %v after %d calls and %d requests, want the handler's error after 1 and 1", err, calls, s.requests())
	}
}

func TestSubscribeNeedsATopic(t *testing.T) {
	c, _ := New("http://localhost:1")
	if err := c.Subscribe(context.Background(), nil, SubscribeOptions{}, func(message.Message) error { return nil }); err == nil {
		t.Error("no topics was accepted")
	}
}

func TestASilentConnectionIsTreatedAsDead(t *testing.T) {
	m := msg("after the silence")
	c, s := start(t,
		hang, // sends only headers, then nothing: not even keepalives
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			send(w, open("cp"), m)
			<-r.Context().Done()
		})
	c.IdleTimeout = 80 * time.Millisecond
	var reasons []string
	c.OnRetry = func(err error, _ time.Duration) { reasons = append(reasons, err.Error()) }
	col := subscribe(t, c, SubscribeOptions{})
	if got := col.waitFor(t, 1); got[0] != "after the silence" {
		t.Errorf("got %v", got)
	}
	col.finish(t)
	if s.requests() != 2 || len(reasons) != 1 || !strings.Contains(reasons[0], "silent") {
		t.Errorf("%d requests, retry reasons %v", s.requests(), reasons)
	}
}

func TestKeepalivesKeepAConnectionAlive(t *testing.T) {
	c, s := start(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		send(w, open("cp"))
		for {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(30 * time.Millisecond):
				send(w, message.Message{Event: "keepalive"})
			}
		}
	})
	c.IdleTimeout = 100 * time.Millisecond
	col := subscribe(t, c, SubscribeOptions{})
	time.Sleep(400 * time.Millisecond) // four idle timeouts' worth
	col.finish(t)
	if s.requests() != 1 {
		t.Errorf("reconnected %d times although keepalives kept arriving", s.requests()-1)
	}
}

func TestPollReturnsAfterTheCachedMessages(t *testing.T) {
	c, s := start(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); send(w, msg("a"), msg("b")) })
	var got []string
	err := c.Subscribe(context.Background(), []string{"t", "u"}, SubscribeOptions{Poll: true, Since: "10m"}, func(m message.Message) error {
		got = append(got, m.Message)
		return nil
	})
	if err != nil || strings.Join(got, ",") != "a,b" {
		t.Errorf("err = %v, got %v", err, got)
	}
	if s.query(0) != "poll=1&since=10m" || s.requests() != 1 {
		t.Errorf("query %q after %d requests", s.query(0), s.requests())
	}
}

func TestPollReportsErrorsAndHandlerFailures(t *testing.T) {
	c, _ := start(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"code":40002,"http":400,"error":"invalid since value"}`))
	})
	err := c.Subscribe(context.Background(), []string{"t"}, SubscribeOptions{Poll: true, Since: "x"}, func(message.Message) error { return nil })
	var api *APIError
	if !errors.As(err, &api) || api.Code != 40002 {
		t.Errorf("err = %v", err)
	}

	boom := errors.New("stop")
	c, _ = start(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); send(w, msg("a"), msg("b")) })
	err = c.Subscribe(context.Background(), []string{"t"}, SubscribeOptions{Poll: true}, func(message.Message) error { return boom })
	if err != boom {
		t.Errorf("err = %v, want the handler's", err)
	}
}

func TestBackoffGrowsThenCapsAndResetsAfterASuccess(t *testing.T) {
	fail := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }
	good := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); send(w, open("cp")) } // connects, then drops
	c, s := start(t, fail, fail, fail, fail, fail, good, fail)
	c.MinBackoff, c.MaxBackoff = 20*time.Millisecond, 80*time.Millisecond
	col := subscribe(t, c, SubscribeOptions{})
	for s.requests() < 8 {
		time.Sleep(5 * time.Millisecond)
	}
	col.finish(t)

	s.mu.Lock()
	defer s.mu.Unlock()
	gap := func(i int) time.Duration { return s.times[i+1].Sub(s.times[i]) }
	// Waits are about 20, 40, 80, 80 (capped), then 20 again after the good connection.
	if !(gap(2) > gap(0)+15*time.Millisecond) {
		t.Errorf("waits should grow: %v then %v", gap(0), gap(2))
	}
	if gap(4) > 150*time.Millisecond {
		t.Errorf("a wait of %v exceeds the 80ms cap by too much", gap(4))
	}
	if gap(6) > 55*time.Millisecond {
		t.Errorf("after a connection that worked the wait was %v, want it back near the 20ms minimum", gap(6))
	}
}
