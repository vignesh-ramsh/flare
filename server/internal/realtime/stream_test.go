package realtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/id"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/store"
)

// streamServer serves one stream endpoint over real HTTP. handlerDone receives a
// value each time a handler returns, to prove that streams end.
func streamServer(t *testing.T, serve func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, chan error) {
	t.Helper()
	done := make(chan error, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r)
		done <- nil
	}))
	t.Cleanup(srv.Close)
	return srv, done
}

func get(t *testing.T, url string) (*http.Response, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	return resp, bufio.NewReader(resp.Body), cancel
}

func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the stream: %v (got %q)", err, line)
	}
	return line
}

func decode(t *testing.T, line string) message.Message {
	t.Helper()
	var m message.Message
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("not a JSON message: %q: %v", line, err)
	}
	return m
}

func expectDone(t *testing.T, done chan error) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return after the client went away")
	}
}

func TestJSONStream(t *testing.T) {
	h, _ := newHub(t, Options{})
	old := mustPublish(t, h, "t", "from the backlog")
	srv, done := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
		ServeJSON(w, r, h, []string{"t", "u"}, AllCursor, StreamOptions{})
	})

	resp, body, cancel := get(t, srv.URL)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-ndjson") {
		t.Errorf("status %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Cache-Control") != "no-cache" || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("headers that keep proxies from buffering the stream are missing: %v", resp.Header)
	}

	open := decode(t, readLine(t, body))
	if open.Event != "open" || open.Topic != "t,u" || !id.Valid(open.ID) {
		t.Errorf("first line = %+v, want an open event for t,u carrying a valid ID", open)
	}
	if got := decode(t, readLine(t, body)); got.ID != old.ID || got.Event != "message" {
		t.Errorf("second line = %+v, want the backlog message", got)
	}
	fresh := mustPublish(t, h, "u", "live")
	if got := decode(t, readLine(t, body)); got.ID != fresh.ID || got.Message != "live" {
		t.Errorf("third line = %+v, want the live message", got)
	}
	cancel()
	expectDone(t, done)
}

func TestSSEStream(t *testing.T) {
	h, _ := newHub(t, Options{})
	m := mustPublish(t, h, "t", "hello")
	srv, done := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
		ServeSSE(w, r, h, []string{"t"}, AllCursor, StreamOptions{})
	})
	resp, body, cancel := get(t, srv.URL)
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Errorf("content type %q", resp.Header.Get("Content-Type"))
	}

	frame := func() []string { // one event: lines up to the blank line
		var lines []string
		for {
			line := strings.TrimSuffix(readLine(t, body), "\n")
			if line == "" {
				return lines
			}
			lines = append(lines, line)
		}
	}

	open := frame()
	if len(open) != 2 || open[0] != "event: open" || !strings.HasPrefix(open[1], "data: {") {
		t.Fatalf("open frame = %q; it must have an event and data line and no id", open)
	}
	msg := frame()
	if len(msg) != 3 || msg[0] != "id: "+m.ID || msg[1] != "event: message" || !strings.HasPrefix(msg[2], "data: ") {
		t.Fatalf("message frame = %q, want id, event and data lines", msg)
	}
	var got message.Message
	if err := json.Unmarshal([]byte(strings.TrimPrefix(msg[2], "data: ")), &got); err != nil || got.ID != m.ID || got.Message != "hello" {
		t.Errorf("data = %q (%v)", msg[2], err)
	}
	cancel()
	expectDone(t, done)
}

func TestSSEKeepsMultilineBodiesOnOneDataLine(t *testing.T) {
	h, _ := newHub(t, Options{})
	mustPublish(t, h, "t", "line one\nline two\r\nline three")
	srv, done := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
		ServeSSE(w, r, h, []string{"t"}, AllCursor, StreamOptions{})
	})
	_, body, cancel := get(t, srv.URL)
	for range 3 {
		readLine(t, body) // open frame
	}
	readLine(t, body) // id
	readLine(t, body) // event
	data := readLine(t, body)
	if !strings.HasPrefix(data, "data: {") || strings.Count(data, "\n") != 1 {
		t.Errorf("data line = %q: newlines in the body must stay escaped inside the JSON", data)
	}
	if readLine(t, body) != "\n" {
		t.Error("frame was not terminated by a blank line")
	}
	cancel()
	expectDone(t, done)
}

func TestKeepalivesFillQuietStretches(t *testing.T) {
	h, _ := newHub(t, Options{})
	srv, done := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
		ServeJSON(w, r, h, []string{"t"}, LiveCursor, StreamOptions{Heartbeat: 20 * time.Millisecond})
	})
	_, body, cancel := get(t, srv.URL)
	readLine(t, body) // open
	for range 3 {
		if ka := decode(t, readLine(t, body)); ka.Event != "keepalive" || ka.Topic != "t" || ka.ID != "" || ka.Time == 0 {
			t.Errorf("keepalive = %+v: want an event with a time and topic, and no ID that could be mistaken for a cursor", ka)
		}
	}
	cancel()
	expectDone(t, done)
}

func TestPoll(t *testing.T) {
	h, _ := newHub(t, Options{})
	a := mustPublish(t, h, "t", "a")
	b := mustPublish(t, h, "t", "b")
	mustPublish(t, h, "other", "x")

	poll := func(after string) (*http.Response, []message.Message) {
		srv, _ := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
			if started, err := ServePoll(w, r, h, []string{"t"}, after, StreamOptions{}); err != nil || !started {
				t.Errorf("started=%v err=%v", started, err)
			}
		})
		resp, body, _ := get(t, srv.URL)
		var out []message.Message
		for {
			line, err := body.ReadString('\n')
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, decode(t, line))
		}
		return resp, out
	}

	resp, got := poll("")
	if resp.StatusCode != 200 || len(got) != 2 || got[0].ID != a.ID || got[1].ID != b.ID {
		t.Errorf("poll = %d %v, want both messages of t and then the end of the response", resp.StatusCode, ids(got))
	}
	for _, m := range got {
		if m.Event == "open" {
			t.Error("a poll must not contain an open event")
		}
	}
	if _, got = poll(a.ID); len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("poll after a = %v, want only b", ids(got))
	}
	resp, got = poll(b.ID)
	if resp.StatusCode != 200 || len(got) != 0 {
		t.Errorf("poll with nothing new = %d %v, want an empty 200", resp.StatusCode, ids(got))
	}
}

type brokenStore struct{ store.Store }

func (brokenStore) After(context.Context, []string, string, time.Time, int) ([]message.Message, error) {
	return nil, errors.New("database is down")
}

func TestPollReportsAFailureBeforeAnythingWasWritten(t *testing.T) {
	h := NewHub(brokenStore{}, nil, Options{})
	rec := httptest.NewRecorder()
	started, err := ServePoll(rec, httptest.NewRequest("GET", "/", nil), h, []string{"t"}, "", StreamOptions{})
	if err == nil || started {
		t.Errorf("started=%v err=%v; want an error and nothing written, so the caller can still answer with a 500", started, err)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("wrote %q before failing", rec.Body.String())
	}
}

// --- WebSocket ---------------------------------------------------------------

type wsClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func dialWS(t *testing.T, url string) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return &wsClient{t: t, conn: conn}
}

func (c *wsClient) send(v any) {
	c.t.Helper()
	data, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatal(err)
	}
}

func (c *wsClient) read() map[string]any {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		c.t.Fatalf("reading a frame: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		c.t.Fatalf("frame %q is not a JSON object: %v", data, err)
	}
	return out
}

func (c *wsClient) expect(event string) map[string]any {
	c.t.Helper()
	f := c.read()
	if f["event"] != event {
		c.t.Fatalf("got frame %v, want a %q event", f, event)
	}
	return f
}

// expectBoth reads two frames that may arrive in either order and checks that
// they are the two events asked for. A connection that publishes to a topic it
// also follows gets the acknowledgement and its own message, and nothing says
// which comes first: the message is sent by the subscription, the
// acknowledgement by the request that published it.
func (c *wsClient) expectBoth(a, b string) map[string]map[string]any {
	c.t.Helper()
	first, second := c.read(), c.read()
	got := map[string]map[string]any{fmt.Sprint(first["event"]): first, fmt.Sprint(second["event"]): second}
	if len(got) != 2 || got[a] == nil || got[b] == nil {
		c.t.Fatalf("got frames %v and %v, want a %q and a %q in either order", first, second, a, b)
	}
	return got
}

func (c *wsClient) expectError(code int, mention string) {
	c.t.Helper()
	f := c.expect("error")
	if int(f["code"].(float64)) != code || !strings.Contains(f["error"].(string), mention) {
		c.t.Fatalf("got error %v, want code %d mentioning %q", f, code, mention)
	}
}

// allowAll is an Authorize that permits everything.
func allowAll(context.Context, auth.Identity, string, auth.Permission) error { return nil }

func wsServer(t *testing.T, h *Hub, opt WSOptions) *httptest.Server {
	t.Helper()
	if opt.Authorize == nil {
		opt.Authorize = allowAll
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(w, r, h, opt)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWebSocketPublishAndSubscribe(t *testing.T) {
	h, _ := newHub(t, Options{})
	old := mustPublish(t, h, "alerts", "stored earlier")
	srv := wsServer(t, h, WSOptions{})
	sub, pub := dialWS(t, srv.URL), dialWS(t, srv.URL)

	sub.send(map[string]any{"op": "subscribe", "topic": "alerts"})
	if f := sub.expect("subscribed"); f["topic"] != "alerts" {
		t.Errorf("subscribed frame = %v", f)
	}

	pub.send(map[string]any{"op": "publish", "topic": "alerts", "message": "disk full", "title": "Alert", "priority": 5, "tags": []string{"warning"}})
	ack := pub.expect("published")
	if !id.Valid(ack["id"].(string)) || ack["topic"] != "alerts" {
		t.Errorf("published frame = %v", ack)
	}

	got := sub.expect("message")
	if got["id"] != ack["id"] || got["message"] != "disk full" || got["title"] != "Alert" || got["priority"] != float64(5) {
		t.Errorf("message frame = %v, want the one just published", got)
	}
	_ = old // a live subscription (no since) must not replay it; the next test covers catch-up
}

func TestWebSocketSinceReplaysTheBacklog(t *testing.T) {
	h, _ := newHub(t, Options{})
	a := mustPublish(t, h, "t", "a")
	b := mustPublish(t, h, "t", "b")
	srv := wsServer(t, h, WSOptions{})
	c := dialWS(t, srv.URL)

	c.send(map[string]any{"op": "subscribe", "topic": "t", "since": a.ID})
	c.expect("subscribed")
	if f := c.expect("message"); f["id"] != b.ID {
		t.Errorf("replayed %v, want only %s", f["id"], b.ID)
	}
	c.send(map[string]any{"op": "subscribe", "topic": "other", "since": "all"})
	c.expect("subscribed")
}

func TestWebSocketMultiplexesTopicsAndUnsubscribes(t *testing.T) {
	h, _ := newHub(t, Options{})
	srv := wsServer(t, h, WSOptions{})
	c := dialWS(t, srv.URL)
	for _, topic := range []string{"a", "b"} {
		c.send(map[string]any{"op": "subscribe", "topic": topic})
		c.expect("subscribed")
	}
	mustPublish(t, h, "a", "to a")
	mustPublish(t, h, "b", "to b")
	topics := map[string]bool{}
	for range 2 {
		topics[c.expect("message")["topic"].(string)] = true
	}
	if !topics["a"] || !topics["b"] {
		t.Errorf("received on %v, want both a and b over one connection", topics)
	}

	c.send(map[string]any{"op": "unsubscribe", "topic": "a"})
	if f := c.expect("unsubscribed"); f["topic"] != "a" {
		t.Errorf("unsubscribed frame = %v", f)
	}
	mustPublish(t, h, "a", "no longer wanted")
	later := mustPublish(t, h, "b", "still wanted")
	if f := c.expect("message"); f["id"] != later.ID {
		t.Errorf("got %v: a message on an unsubscribed topic was delivered", f)
	}
	c.send(map[string]any{"op": "unsubscribe", "topic": "a"})
	c.expectError(apierr.NotFound, "not subscribed")
}

func TestWebSocketReportsMistakesAndKeepsGoing(t *testing.T) {
	h, _ := newHub(t, Options{MaxMessageSize: 20})
	srv := wsServer(t, h, WSOptions{MaxSubscriptions: 2})
	c := dialWS(t, srv.URL)

	c.send(map[string]any{"op": "subscribe", "topic": "bad/topic"})
	c.expectError(apierr.InvalidTopic, "invalid topic")
	c.send(map[string]any{"op": "subscribe", "topic": "v1"})
	c.expectError(apierr.InvalidTopic, "invalid topic")
	c.send(map[string]any{"op": "subscribe", "topic": "t", "since": "banana"})
	c.expectError(apierr.InvalidParameter, "invalid since")
	c.send(map[string]any{"op": "dance", "topic": "t"})
	c.expectError(apierr.InvalidParameter, "unknown op")
	c.send(map[string]any{"op": "auth", "token": "x"})
	c.expectError(apierr.Unauthorized, "no accounts")

	c.send(map[string]any{"op": "publish", "topic": "t", "message": strings.Repeat("x", 21)})
	c.expectError(apierr.TooLarge, "limit of 20 bytes")
	c.send(map[string]any{"op": "publish", "topic": "t", "message": "x", "priority": 9})
	c.expectError(apierr.InvalidParameter, "priority")
	c.send(map[string]any{"op": "publish", "topic": "t", "message": "x", "click": "javascript:alert(1)"})
	c.expectError(apierr.InvalidParameter, "scheme")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.conn.Write(ctx, websocket.MessageText, []byte("{not json"))
	c.expectError(apierr.InvalidBody, "not valid JSON")
	c.conn.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3})
	c.expectError(apierr.InvalidBody, "JSON text")

	c.send(map[string]any{"op": "subscribe", "topic": "a"})
	c.expect("subscribed")
	c.send(map[string]any{"op": "subscribe", "topic": "a"})
	c.expectError(apierr.AlreadySubscribed, "already")
	c.send(map[string]any{"op": "subscribe", "topic": "b"})
	c.expect("subscribed")
	c.send(map[string]any{"op": "subscribe", "topic": "c"})
	c.expectError(apierr.TooMany, "too many")

	// Nothing above closed the connection or stored anything.
	c.send(map[string]any{"op": "publish", "topic": "t", "message": "fine"})
	c.expect("published")
	if got := replay(t, h, []string{"t"}, ""); len(got) != 1 {
		t.Errorf("%d messages stored, want only the one valid publish", len(got))
	}
}

func TestWebSocketAuthorizesEveryOperation(t *testing.T) {
	h, _ := newHub(t, Options{})
	srv := wsServer(t, h, WSOptions{Authorize: func(_ context.Context, _ auth.Identity, topic string, perm auth.Permission) error {
		if topic == "secret" || perm == auth.Write && topic == "readonly" {
			return fmt.Errorf("%w: no %s access to %s", auth.ErrForbidden, perm, topic)
		}
		return nil
	}})
	c := dialWS(t, srv.URL)

	c.send(map[string]any{"op": "subscribe", "topic": "secret"})
	c.expectError(apierr.Forbidden, "no read access to secret")
	c.send(map[string]any{"op": "publish", "topic": "secret", "message": "x"})
	c.expectError(apierr.Forbidden, "no write access")
	c.send(map[string]any{"op": "subscribe", "topic": "readonly"})
	c.expect("subscribed")
	c.send(map[string]any{"op": "publish", "topic": "readonly", "message": "x"})
	c.expectError(apierr.Forbidden, "no write access to readonly")
	if got := replay(t, h, []string{"secret", "readonly"}, ""); len(got) != 0 {
		t.Errorf("a forbidden publish was stored: %v", ids(got))
	}
}

func TestWebSocketRefusesOversizedFramesByClosing(t *testing.T) {
	h, _ := newHub(t, Options{MaxMessageSize: 100})
	srv := wsServer(t, h, WSOptions{MaxMessageSize: 100})
	c := dialWS(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.conn.Write(ctx, websocket.MessageText, []byte(`{"op":"publish","topic":"t","message":"`+strings.Repeat("x", 200_000)+`"}`))
	_, _, err := c.conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Errorf("read error %v (status %d), want the connection closed as too big", err, websocket.CloseStatus(err))
	}
}

func TestWebSocketDisconnectEndsEverythingForThatClient(t *testing.T) {
	h, _ := newHub(t, Options{})
	finished := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(w, r, h, WSOptions{Authorize: allowAll})
		close(finished)
	}))
	defer srv.Close()
	c := dialWS(t, srv.URL)
	c.send(map[string]any{"op": "subscribe", "topic": "t"})
	c.expect("subscribed")
	c.conn.Close(websocket.StatusNormalClosure, "bye")
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the server kept the connection after the client closed it")
	}
}

func TestWebSocketServerShutdownClosesWithGoingAway(t *testing.T) {
	h, _ := newHub(t, Options{})
	base, shutdown := context.WithCancel(context.Background())
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(w, r, h, WSOptions{Authorize: allowAll})
	}))
	srv.Config.BaseContext = func(net.Listener) context.Context { return base }
	srv.Start()
	defer srv.Close()

	c := dialWS(t, srv.URL)
	c.send(map[string]any{"op": "subscribe", "topic": "t"})
	c.expect("subscribed")
	shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Errorf("read error %v (status %d), want a close with status going-away", err, websocket.CloseStatus(err))
	}
}
