package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"gopkg.in/yaml.v3"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/broker"
	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/id"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/realtime"
	"github.com/vignesh-ramsh/flare/server/internal/settings"
	"github.com/vignesh-ramsh/flare/server/internal/store"
	"github.com/vignesh-ramsh/flare/server/internal/theme"
)

// env is a complete server (real store, broker, hub and API) behind a test HTTP
// server.
type env struct {
	t     *testing.T
	srv   *httptest.Server
	api   *API
	hub   *realtime.Hub
	store *store.SQLite
	auth  *auth.Service
	cfg   config.Config
	logs  *syncBuffer

	clients int // client addresses handed out by asNewClient
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newEnv(t *testing.T, edit ...func(*config.Config)) *env {
	t.Helper()
	cfg := config.Default()
	cfg.BaseURL = "https://flare.example.com"
	cfg.MessageSizeLimit = 200
	for _, e := range edit {
		e(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "flare.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := realtime.NewHub(st, broker.NewInProc(), realtime.Options{TTL: cfg.CacheDuration, MaxMessageSize: cfg.MessageSizeLimit})
	access, _ := auth.ParseAccess(cfg.Auth.DefaultAccess)
	proxies, err := auth.ParseProxies(cfg.TrustedProxies)
	if err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(st, auth.ServiceConfig{
		Default: access, Proxies: proxies,
		HashParams: auth.Params{Time: 1, MemoryKiB: 64, Threads: 1}, // cheap, for tests
	})
	logs := &syncBuffer{}
	live, err := settings.New(context.Background(), st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	a := New(Deps{
		Hub: hub, Auth: svc, Config: cfg, Themes: theme.NewService(st), Settings: live,
		Log:            slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Stream:         realtime.StreamOptions{Heartbeat: 40 * time.Millisecond},
		WSPingInterval: 40 * time.Millisecond,
	})
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, api: a, hub: hub, store: st, auth: svc, cfg: cfg, logs: logs}
}

func access(level auth.Access) func(*config.Config) {
	return func(c *config.Config) { c.Auth.DefaultAccess = string(level) }
}

func (e *env) do(method, path, body string, headers ...string) (*http.Response, []byte) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func decodeMsg(t *testing.T, data []byte) message.Message {
	t.Helper()
	var m message.Message
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("not a message: %q: %v", data, err)
	}
	return m
}

// published publishes with POST and requires success.
func (e *env) published(topic, body string, headers ...string) message.Message {
	e.t.Helper()
	resp, data := e.do("POST", "/"+topic, body, headers...)
	if resp.StatusCode != 200 {
		e.t.Fatalf("publish: %d %s", resp.StatusCode, data)
	}
	return decodeMsg(e.t, data)
}

func (e *env) expectError(resp *http.Response, data []byte, code int, mention string) {
	e.t.Helper()
	var body struct {
		Code  int    `json:"code"`
		HTTP  int    `json:"http"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		e.t.Fatalf("error response is not JSON: %q", data)
	}
	if resp.StatusCode != apierr.HTTPStatus(code) || body.Code != code || body.HTTP != resp.StatusCode ||
		!strings.Contains(body.Error, mention) {
		e.t.Fatalf("got %d %+v, want status %d, code %d, an error mentioning %q", resp.StatusCode, body, apierr.HTTPStatus(code), code, mention)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		e.t.Errorf("error content type %q", resp.Header.Get("Content-Type"))
	}
}

// poll returns the cached messages as a poll request would.
func (e *env) poll(path string, headers ...string) []message.Message {
	e.t.Helper()
	resp, data := e.do("GET", path, "", headers...)
	if resp.StatusCode != 200 {
		e.t.Fatalf("poll %s: %d %s", path, resp.StatusCode, data)
	}
	var out []message.Message
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			out = append(out, decodeMsg(e.t, []byte(line)))
		}
	}
	return out
}

func bodies(ms []message.Message) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Message)
	}
	return out
}

// --- Publishing --------------------------------------------------------------

func TestPublishLikeCurl(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do("POST", "/alerts", "disk is full", "Title", "Alert", "Priority", "high", "Tags", "warning, disk", "Click", "https://example.com/d")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("%d %s (%s)", resp.StatusCode, data, resp.Header.Get("Content-Type"))
	}
	m := decodeMsg(t, data)
	if !id.Valid(m.ID) || m.Event != "message" || m.Topic != "alerts" || m.Message != "disk is full" || m.Title != "Alert" ||
		m.Priority != 4 || strings.Join(m.Tags, "|") != "warning|disk" || m.Click != "https://example.com/d" {
		t.Errorf("response = %+v", m)
	}
	if m.Time == 0 || m.Expires != m.Time+int64(e.cfg.CacheDuration.Seconds()) {
		t.Errorf("time %d, expires %d: expires should be time plus the cache duration", m.Time, m.Expires)
	}
	if got := e.poll("/alerts/json?poll=1"); len(got) != 1 || got[0].ID != m.ID {
		t.Errorf("the message was not stored: %v", got)
	}
}

func TestPublishWithPUTAndNoSettings(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do("PUT", "/t", "via put")
	m := decodeMsg(t, data)
	if resp.StatusCode != 200 || m.Message != "via put" || m.Priority != 3 || m.Title != "" || m.Tags != nil {
		t.Errorf("%d %+v", resp.StatusCode, m)
	}
}

func TestPublishSettingsFromQueryParameters(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do("POST", "/t?title=Hi&priority=5&tags=a,b&click=https://example.com", "x")
	m := decodeMsg(t, data)
	if resp.StatusCode != 200 || m.Title != "Hi" || m.Priority != 5 || len(m.Tags) != 2 || m.Click != "https://example.com" {
		t.Errorf("%d %+v", resp.StatusCode, m)
	}
	// A header wins over the query parameter.
	m = e.published("t?title=FromQuery", "x", "Title", "FromHeader")
	if m.Title != "FromHeader" {
		t.Errorf("title = %q, want the header's", m.Title)
	}
}

func TestPublishDecodesEncodedHeaders(t *testing.T) {
	e := newEnv(t)
	m := e.published("t", "x", "Title", "=?UTF-8?B?Q2Fmw6kg4pyT?=", "Tags", "=?UTF-8?Q?caf=C3=A9?=,plain")
	if m.Title != "Café ✓" || strings.Join(m.Tags, "|") != "café|plain" {
		t.Errorf("title %q, tags %q", m.Title, m.Tags)
	}
	// Text that only looks like an encoded word is kept as it is...
	if m := e.published("t", "x", "Title", "=?UTF-8?B?***?="); m.Title != "=?UTF-8?B?***?=" {
		t.Errorf("title = %q, want the text unchanged", m.Title)
	}
	// ...but a charset that can't be decoded is refused rather than guessed at.
	resp, data := e.do("POST", "/t", "x", "Title", "=?x-unknown?Q?caf=E9?=")
	e.expectError(resp, data, apierr.InvalidParameter, "title")
}

func TestPublishWithNothingGivesTriggered(t *testing.T) {
	e := newEnv(t)
	if m := e.published("t", ""); m.Message != "triggered" {
		t.Errorf("message = %q", m.Message)
	}
	if m := e.published("t", "", "Title", "Only a title"); m.Message != "" || m.Title != "Only a title" {
		t.Errorf("%+v", m)
	}
}

func TestPublishPreservesMultilineAndUnicodeBodies(t *testing.T) {
	e := newEnv(t)
	body := "first line\nsecond line\r\nthird — ünïcode ✓ 🔥"
	m := e.published("t", body)
	if m.Message != body {
		t.Errorf("message = %q", m.Message)
	}
	if got := e.poll("/t/json?poll=1"); got[0].Message != body {
		t.Errorf("stored message = %q", got[0].Message)
	}
}

func TestPublishRefusals(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		headers []string
		code    int
		mention string
	}{
		{"topic with a dot", "POST", "/bad.topic", "x", nil, apierr.InvalidTopic, "invalid topic"},
		{"topic that is too long", "POST", "/" + strings.Repeat("a", 65), "x", nil, apierr.InvalidTopic, "invalid topic"},
		{"reserved topic v1", "POST", "/v1", "x", nil, apierr.InvalidTopic, "reserved"},
		{"reserved topic healthz", "PUT", "/healthz", "x", nil, apierr.InvalidTopic, "reserved"},
		{"body over the limit", "POST", "/t", strings.Repeat("x", 201), nil, apierr.TooLarge, "limit of 200 bytes"},
		{"body far over the limit", "POST", "/t", strings.Repeat("x", 100_000), nil, apierr.TooLarge, "limit of 200 bytes"},
		{"body that is not text", "POST", "/t", "\xff\xfe\xfd", nil, apierr.InvalidParameter, "UTF-8"},
		{"priority out of range", "POST", "/t", "x", []string{"Priority", "9"}, apierr.InvalidParameter, "out of range"},
		{"priority that is a word", "POST", "/t", "x", []string{"Priority", "loud"}, apierr.InvalidParameter, "priority"},
		{"click with javascript", "POST", "/t", "x", []string{"Click", "javascript:alert(1)"}, apierr.InvalidParameter, "scheme"},
		{"too many tags", "POST", "/t", "x", []string{"Tags", strings.TrimSuffix(strings.Repeat("a,", 21), ",")}, apierr.InvalidParameter, "tags"},
		{"title that is too long", "POST", "/t", "x", []string{"Title", strings.Repeat("t", 251)}, apierr.InvalidParameter, "title"},
		{"delay is not supported", "POST", "/t", "x", []string{"Delay", "30m"}, apierr.Unsupported, "Delay is not supported yet"},
		{"attach is not supported", "POST", "/t", "x", []string{"Attach", "https://example.com/f.png"}, apierr.Unsupported, "Attach"},
		{"actions are not supported", "POST", "/t", "x", []string{"Actions", "view, Open, https://example.com"}, apierr.Unsupported, "Actions"},
		{"unsupported setting as a query parameter", "POST", "/t?delay=30m", "x", nil, apierr.Unsupported, "Delay"},
		{"markdown is not supported", "POST", "/t", "x", []string{"Markdown", "yes"}, apierr.Unsupported, "Markdown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, data := e.do(tt.method, tt.path, tt.body, tt.headers...)
			e.expectError(resp, data, tt.code, tt.mention)
		})
	}
	if got := e.poll("/t/json?poll=1"); len(got) != 0 {
		t.Errorf("%d refused messages were stored: %v", len(got), bodies(got))
	}
}

func TestPublishWithOtherMethodsIsNotAllowed(t *testing.T) {
	e := newEnv(t)
	for _, method := range []string{"DELETE", "PATCH"} {
		resp, _ := e.do(method, "/t", "x")
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s /t = %d, want 405", method, resp.StatusCode)
		}
	}
}

func TestPublishJSON(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do("POST", "/v1/messages", `{"topic":"alerts","message":"hi","title":"T","priority":2,"tags":["a","b"],"click":"https://example.com"}`)
	m := decodeMsg(t, data)
	if resp.StatusCode != 200 || m.Topic != "alerts" || m.Message != "hi" || m.Title != "T" || m.Priority != 2 || len(m.Tags) != 2 {
		t.Errorf("%d %+v", resp.StatusCode, m)
	}
	if got := e.poll("/alerts/json?poll=1"); len(got) != 1 {
		t.Errorf("not stored: %v", got)
	}
}

func TestPublishJSONRefusals(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name, body string
		code       int
		mention    string
	}{
		{"not JSON", `{"topic":`, apierr.InvalidBody, "invalid JSON"},
		{"empty body", ``, apierr.InvalidBody, "invalid JSON"},
		{"unknown field", `{"topic":"t","delay":"5m"}`, apierr.InvalidBody, `unknown field "delay"`},
		{"trailing data", `{"topic":"t"} {"topic":"u"}`, apierr.InvalidBody, "unexpected data"},
		{"wrong type", `{"topic":"t","priority":"high"}`, apierr.InvalidBody, "invalid JSON"},
		{"no topic", `{"message":"x"}`, apierr.InvalidTopic, "invalid topic"},
		{"bad topic", `{"topic":"a/b"}`, apierr.InvalidTopic, "invalid topic"},
		{"reserved topic", `{"topic":"v1"}`, apierr.InvalidTopic, "reserved"},
		{"message over the limit", `{"topic":"t","message":"` + strings.Repeat("x", 201) + `"}`, apierr.TooLarge, "limit of 200 bytes"},
		{"request far over the limit", `{"topic":"t","message":"` + strings.Repeat("x", 100_000) + `"}`, apierr.TooLarge, "larger than"},
		{"priority out of range", `{"topic":"t","priority":9}`, apierr.InvalidParameter, "priority"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, data := e.do("POST", "/v1/messages", tt.body)
			e.expectError(resp, data, tt.code, tt.mention)
		})
	}
}

// --- Subscribing -------------------------------------------------------------

func TestSubscribeFromAGivenPoint(t *testing.T) {
	e := newEnv(t)
	a := e.published("t", "a")
	b := e.published("t", "b")
	c := e.published("t", "c")
	e.published("other", "elsewhere")

	check := func(path string, want ...string) {
		t.Helper()
		if got := bodies(e.poll(path)); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %v, want %v", path, got, want)
		}
	}
	check("/t/json?poll=1", "a", "b", "c") // a poll defaults to everything cached
	check("/t/json?poll=1&since=all", "a", "b", "c")
	check("/t/json?poll=1&since="+a.ID, "b", "c")
	check("/t/json?poll=1&since=" + c.ID)
	check("/t/json?poll=true&since=1h", "a", "b", "c")
	check("/t/json?poll=yes&since=1h", "a", "b", "c")
	check(fmt.Sprintf("/t/json?poll=1&since=%d", time.Now().Add(time.Hour).Unix()))
	check("/t,other/json?poll=1&since="+b.ID, "c", "elsewhere")
	check("/t,t,other/json?poll=1&since="+b.ID, "c", "elsewhere") // repeats are harmless
	_ = b
}

func TestSubscribeRefusals(t *testing.T) {
	e := newEnv(t)
	for _, tt := range []struct {
		path    string
		code    int
		mention string
	}{
		{"/t/json?since=banana", apierr.InvalidParameter, "invalid since"},
		{"/t/json?poll=1&since=30", apierr.InvalidParameter, "need a unit"},
		{"/t/sse?since=-5m", apierr.InvalidParameter, "invalid since"},
		{"/bad.topic/json", apierr.InvalidTopic, "invalid topic"},
		{"/a,,b/json", apierr.InvalidTopic, "invalid topic"},
		{"/v1/json", apierr.InvalidTopic, "invalid topic"},
		{"/" + strings.Join(manyTopics(51), ",") + "/json", apierr.InvalidTopic, "too many"},
	} {
		resp, data := e.do("GET", tt.path, "")
		e.expectError(resp, data, tt.code, tt.mention)
	}
}

func manyTopics(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("topic%02d", i)
	}
	return out
}

func openStream(t *testing.T, url string, headers ...string) (*http.Response, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	return resp, bufio.NewReader(resp.Body), cancel
}

func nextEvent(t *testing.T, r *bufio.Reader) message.Message {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the stream: %v", err)
	}
	return decodeMsg(t, []byte(line))
}

func TestStreamingLiveMessagesAndKeepalives(t *testing.T) {
	e := newEnv(t)
	resp, body, _ := openStream(t, e.srv.URL+"/news/json")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if open := nextEvent(t, body); open.Event != "open" || open.Topic != "news" {
		t.Fatalf("first event = %+v", open)
	}
	sent := e.published("news", "breaking")
	for {
		ev := nextEvent(t, body)
		if ev.Event == "keepalive" {
			continue
		}
		if ev.ID != sent.ID || ev.Message != "breaking" {
			t.Fatalf("got %+v, want the published message", ev)
		}
		break
	}
	if ka := nextEvent(t, body); ka.Event != "keepalive" {
		t.Errorf("expected keepalives while quiet, got %+v", ka)
	}
}

func TestSSEResumesFromLastEventID(t *testing.T) {
	e := newEnv(t)
	a := e.published("t", "a")
	e.published("t", "b")
	read := func(headers ...string) (events []string) {
		_, body, cancel := openStream(t, e.srv.URL+"/t/sse"+headers[0], headers[1:]...)
		defer cancel()
		for len(events) < 2 {
			line, err := body.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "event: ") {
				events = append(events, strings.TrimSpace(strings.TrimPrefix(line, "event: ")))
			}
		}
		return events
	}
	// Last-Event-ID is what a browser's EventSource sends when it reconnects.
	if got := read("", "Last-Event-ID", a.ID); strings.Join(got, ",") != "open,message" {
		t.Errorf("events = %v, want open then the one message after a", got)
	}
	// An explicit since wins over the header.
	if got := read("?since=all", "Last-Event-ID", a.ID); strings.Join(got, ",") != "open,message" {
		t.Errorf("events = %v", got)
	}
}

func TestWebSocketThroughTheWholeStack(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/v1/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	read := func() map[string]any {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		json.Unmarshal(data, &out)
		return out
	}
	conn.Write(ctx, websocket.MessageText, []byte(`{"op":"subscribe","topic":"t"}`))
	if f := read(); f["event"] != "subscribed" {
		t.Fatalf("got %v", f)
	}
	sent := e.published("t", "over http")
	if f := read(); f["event"] != "message" || f["id"] != sent.ID {
		t.Errorf("got %v, want the message published over HTTP", f)
	}
	conn.Write(ctx, websocket.MessageText, []byte(`{"op":"publish","topic":"t","message":"over ws"}`))
	// The acknowledgement and the message (this connection follows the topic) come in either order.
	first, second := read(), read()
	ack, msg := first, second
	if first["event"] == "message" {
		ack, msg = second, first
	}
	if ack["event"] != "published" || msg["event"] != "message" || msg["message"] != "over ws" || ack["id"] != msg["id"] {
		t.Errorf("got %v and %v, want the acknowledgement and the message, for the same ID", first, second)
	}
	// A connection is logged when it ends, so hang up and wait for the line.
	conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(e.logs.String(), `route="GET /v1/ws" status=101`) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(e.logs.String(), `route="GET /v1/ws" status=101`) {
		t.Errorf("the upgrade should be logged with status 101:\n%s", e.logs.String())
	}
}

func TestEveryMessageIsDeliveredOnceInOrderUnderLoad(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.MessageSizeLimit = 4096 })
	const publishers, each = 8, 25

	_, body, cancel := openStream(t, e.srv.URL+"/load/json")
	defer cancel()
	nextEvent(t, body) // open

	var wg sync.WaitGroup
	var mu sync.Mutex
	var sent []string
	for p := range publishers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				m := e.published("load", fmt.Sprintf("p%d-%d", p, i))
				mu.Lock()
				sent = append(sent, m.ID)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	var got []string
	for len(got) < publishers*each {
		if ev := nextEvent(t, body); ev.Event == "message" {
			got = append(got, ev.ID)
		}
	}
	sort.Strings(sent)
	if strings.Join(got, ",") != strings.Join(sent, ",") {
		t.Errorf("the stream delivered %d messages that differ from, or are out of order with, the %d published", len(got), len(sent))
	}
}

// --- Access ------------------------------------------------------------------

func TestAccessPolicies(t *testing.T) {
	tests := []struct {
		level           auth.Access
		publish, read   bool
		describeAllowed string
	}{
		{auth.ReadWrite, true, true, "everything"},
		{auth.ReadOnly, false, true, "reading only"},
		{auth.WriteOnly, true, false, "writing only"},
		{auth.DenyAll, false, false, "nothing"},
	}
	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			e := newEnv(t, access(tt.level))
			check := func(what string, allowed bool, resp *http.Response, data []byte) {
				t.Helper()
				if allowed && resp.StatusCode != 200 {
					t.Errorf("%s should be allowed with %s, got %d %s", what, tt.describeAllowed, resp.StatusCode, data)
				}
				if !allowed {
					// Nobody is logged in, so the answer is "log in", not "no".
					e.expectError(resp, data, apierr.Unauthorized, "log in")
					if resp.Header.Get("WWW-Authenticate") == "" {
						t.Errorf("%s: a 401 must say how to authenticate", what)
					}
				}
			}
			resp, data := e.do("POST", "/t", "x")
			check("publishing", tt.publish, resp, data)
			resp, data = e.do("POST", "/v1/messages", `{"topic":"t","message":"x"}`)
			check("publishing as JSON", tt.publish, resp, data)
			resp, data = e.do("GET", "/t/json?poll=1", "")
			check("polling", tt.read, resp, data)
			// A live stream never ends, so look at how it starts and hang up.
			resp, body, cancel := openStream(t, e.srv.URL+"/t,u/sse")
			defer cancel()
			if tt.read && resp.StatusCode != 200 {
				t.Errorf("SSE should be allowed, got %d", resp.StatusCode)
			}
			if !tt.read {
				data, _ := io.ReadAll(body)
				e.expectError(resp, data, apierr.Unauthorized, "log in")
			}
		})
	}
}

func TestForbiddenPublishStoresNothing(t *testing.T) {
	e := newEnv(t, access(auth.ReadOnly))
	e.do("POST", "/t", "x")
	e.do("POST", "/v1/messages", `{"topic":"t","message":"x"}`)
	if got := e.poll("/t/json?poll=1"); len(got) != 0 {
		t.Errorf("forbidden publishes were stored: %v", bodies(got))
	}
}

func TestWebSocketFollowsTheAccessPolicy(t *testing.T) {
	e := newEnv(t, access(auth.ReadOnly))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/v1/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	conn.Write(ctx, websocket.MessageText, []byte(`{"op":"publish","topic":"t","message":"x"}`))
	_, data, _ := conn.Read(ctx)
	if !strings.Contains(string(data), `"code":40101`) {
		t.Errorf("got %s, want an error telling the anonymous visitor to log in", data)
	}
	conn.Write(ctx, websocket.MessageText, []byte(`{"op":"subscribe","topic":"t"}`))
	_, data, _ = conn.Read(ctx)
	if !strings.Contains(string(data), `"event":"subscribed"`) {
		t.Errorf("got %s, reading is allowed", data)
	}
}

// --- Information -------------------------------------------------------------

func TestServerInformation(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Branding.Name = "Acme Alerts"; c.Branding.AppURLScheme = "acme-alerts" })

	resp, data := e.do("GET", "/", "")
	if resp.StatusCode != 200 || !strings.Contains(string(data), "Acme Alerts is running") ||
		!strings.Contains(string(data), "https://flare.example.com/mytopic") {
		t.Errorf("index: %d %q", resp.StatusCode, data)
	}
	if resp, data = e.do("GET", "/healthz", ""); resp.StatusCode != 200 || string(data) != "ok\n" {
		t.Errorf("healthz: %d %q", resp.StatusCode, data)
	}
	if resp, data = e.do("GET", "/readyz", ""); resp.StatusCode != 200 || string(data) != "ok\n" {
		t.Errorf("readyz: %d %q", resp.StatusCode, data)
	}

	resp, data = e.do("GET", "/v1/config", "")
	var cfg struct {
		APIVersion   int      `json:"api_version"`
		Name         string   `json:"name"`
		PrimaryColor string   `json:"primary_color"`
		AppURLScheme string   `json:"app_url_scheme"`
		BaseURL      string   `json:"base_url"`
		Features     []string `json:"features"`
		Limits       struct {
			MessageSize   int   `json:"message_size"`
			CacheDuration int64 `json:"cache_duration"`
		} `json:"limits"`
		Auth struct {
			DefaultAccess string `json:"default_access"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %s (%v)", resp.StatusCode, data, err)
	}
	if cfg.APIVersion != 1 || cfg.Name != "Acme Alerts" || cfg.PrimaryColor != "#F97316" || cfg.AppURLScheme != "acme-alerts" ||
		cfg.BaseURL != "https://flare.example.com" || cfg.Limits.MessageSize != 200 || cfg.Limits.CacheDuration != 86400 ||
		cfg.Auth.DefaultAccess != "read-write" || strings.Join(cfg.Features, ",") != "publish,poll,sse,websocket,accounts,themes" {
		t.Errorf("config = %+v", cfg)
	}
	for _, secret := range []string{"sqlite", ".db", "listen"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("/v1/config leaks %q: %s", secret, data)
		}
	}
}

func TestReadyzFailsWhenTheStoreIsGone(t *testing.T) {
	e := newEnv(t)
	e.store.Close()
	resp, data := e.do("GET", "/readyz", "")
	e.expectError(resp, data, apierr.Unavailable, "not available")
	if resp, _ = e.do("GET", "/healthz", ""); resp.StatusCode != 200 {
		t.Errorf("healthz = %d: being unready is not being dead", resp.StatusCode)
	}
}

func TestStoreFailureIsAnInternalErrorWithoutDetails(t *testing.T) {
	e := newEnv(t)
	e.store.Close()
	resp, data := e.do("POST", "/t", "x")
	// The access check reads the same database, so it is what fails first.
	e.expectError(resp, data, apierr.Internal, "internal error")
	if strings.Contains(string(data), "sql") || strings.Contains(string(data), "closed") {
		t.Errorf("internal details leaked to the client: %s", data)
	}
	if !strings.Contains(e.logs.String(), "level=ERROR") {
		t.Errorf("the failure should be logged:\n%s", e.logs.String())
	}
}

func TestUnknownPaths(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/a/b/c", "/t/xml", "/v1/nothing"} {
		if resp, _ := e.do("GET", path, ""); resp.StatusCode != 404 {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestResponsesAreNotSniffedOrCached(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/", "/healthz", "/v1/config", "/bad.topic/json"} {
		resp, _ := e.do("GET", path, "")
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %s lacks X-Content-Type-Options: nosniff", path)
		}
	}
	resp, _ := e.do("GET", "/v1/config", "")
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("config Cache-Control = %q", resp.Header.Get("Cache-Control"))
	}
}

// --- Operations --------------------------------------------------------------

func TestLogsNameTheRouteButNeverTheTopicOrMessage(t *testing.T) {
	e := newEnv(t)
	e.published("a-very-secret-topic", "a very secret message", "Title", "a secret title")
	e.do("GET", "/a-very-secret-topic/json?poll=1&since=all", "")
	e.do("POST", "/v1/messages", `{"topic":"another-secret","message":"another secret body"}`)

	logs := e.logs.String()
	for _, secret := range []string{"secret", "127.0.0.1", "localhost"} {
		if strings.Contains(logs, secret) {
			t.Errorf("the logs contain %q:\n%s", secret, logs)
		}
	}
	for _, want := range []string{`route="POST /{topic}"`, `route="GET /{topics}/json"`, `route="POST /v1/messages"`, "status=200", "duration="} {
		if !strings.Contains(logs, want) {
			t.Errorf("the logs lack %s:\n%s", want, logs)
		}
	}
}

func TestAPanicInAHandlerBecomesAnErrorResponse(t *testing.T) {
	a := New(Deps{Config: config.Default()})
	h := a.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"code":50001`) || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// --- The contract ------------------------------------------------------------

// The OpenAPI file is the contract. If a route is added to the server and not
// to the file, or the other way round, this fails.
func TestRoutesMatchTheOpenAPISpec(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}

	var inSpec []string
	for path, ops := range spec.Paths {
		for method := range ops {
			switch method {
			case "get", "post", "put", "delete", "patch":
				inSpec = append(inSpec, strings.ToUpper(method)+" "+path)
			}
		}
	}
	var inServer []string
	for _, r := range New(Deps{Config: config.Default()}).Routes() {
		inServer = append(inServer, strings.Replace(r, "/{$}", "/", 1)) // the root pattern's spelling
	}
	sort.Strings(inSpec)
	sort.Strings(inServer)
	if strings.Join(inSpec, "\n") != strings.Join(inServer, "\n") {
		t.Errorf("the server and api/openapi.yaml disagree.\nspec:   %v\nserver: %v", inSpec, inServer)
	}
}
