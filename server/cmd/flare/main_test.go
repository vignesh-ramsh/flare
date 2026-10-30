package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/server"
)

type result struct {
	stdout, stderr string
	err            error
}

// run executes the CLI with the given standard input and environment.
func run(t *testing.T, ctx context.Context, stdin string, env map[string]string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	root := newRootCmd(cli{
		stdin: strings.NewReader(stdin), stdout: &out, stderr: &errOut,
		getenv: func(k string) string { return env[k] },
	})
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	return result{out.String(), errOut.String(), err}
}

// realServer starts the actual server behind an httptest server.
func realServer(t *testing.T, edit ...func(*config.Config)) string {
	t.Helper()
	cfg := config.Default()
	cfg.Database = "sqlite://" + filepath.Join(t.TempDir(), "flare.db")
	for _, e := range edit {
		e(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	s, err := server.New(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { srv.Close(); s.Close() })
	return srv.URL
}

func decode(t *testing.T, line string) message.Message {
	t.Helper()
	var m message.Message
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("not a message: %q: %v", line, err)
	}
	return m
}

func TestPublishFromTheCommandLine(t *testing.T) {
	url := realServer(t)
	r := run(t, context.Background(), "", nil, "publish", "-s", url, "--title", "Backup", "-p", "high", "--tags", "warning, disk", "--click", "https://example.com", "backups", "disk", "is", "full")
	if r.err != nil {
		t.Fatal(r.err)
	}
	m := decode(t, r.stdout)
	if m.Topic != "backups" || m.Message != "disk is full" || m.Title != "Backup" || m.Priority != 4 ||
		strings.Join(m.Tags, "|") != "warning|disk" || m.Click != "https://example.com" {
		t.Errorf("published %+v", m)
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestPublishFromStandardInput(t *testing.T) {
	url := realServer(t)
	r := run(t, context.Background(), "line one\nline two\n", nil, "publish", "-s", url, "t")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if m := decode(t, r.stdout); m.Message != "line one\nline two" {
		t.Errorf("message = %q, want the input without its final newline", m.Message)
	}
	// Arguments win over standard input.
	r = run(t, context.Background(), "ignored", nil, "publish", "-s", url, "t", "from args")
	if m := decode(t, r.stdout); m.Message != "from args" {
		t.Errorf("message = %q", m.Message)
	}
}

func TestPublishWithoutAMessageSendsThePlaceholder(t *testing.T) {
	url := realServer(t)
	r := run(t, context.Background(), "", nil, "publish", "-s", url, "t")
	if m := decode(t, r.stdout); r.err != nil || m.Message != "triggered" {
		t.Errorf("%v %+v", r.err, m)
	}
}

func TestPublishQuiet(t *testing.T) {
	url := realServer(t)
	if r := run(t, context.Background(), "", nil, "publish", "-q", "-s", url, "t", "x"); r.err != nil || r.stdout != "" {
		t.Errorf("err %v, stdout %q", r.err, r.stdout)
	}
}

func TestPublishTakesTheServerFromTheEnvironment(t *testing.T) {
	url := realServer(t)
	r := run(t, context.Background(), "", map[string]string{"FLARE_SERVER": url}, "publish", "t", "via env")
	if m := decode(t, r.stdout); r.err != nil || m.Message != "via env" {
		t.Errorf("%v %+v", r.err, m)
	}
	// The flag wins over the environment.
	other := realServer(t)
	run(t, context.Background(), "", map[string]string{"FLARE_SERVER": "http://127.0.0.1:1"}, "publish", "-s", other, "t", "x")
}

func TestPublishErrors(t *testing.T) {
	url := realServer(t, func(c *config.Config) { c.Auth.DefaultAccess = "read-only"; c.MessageSizeLimit = 10 })
	ctx := context.Background()

	if r := run(t, ctx, "", nil, "publish", "-s", url, "t", "x"); r.err == nil || !strings.Contains(r.err.Error(), "does not allow write access") {
		t.Errorf("err = %v, want the server's reason", r.err)
	}
	open := realServer(t, func(c *config.Config) { c.MessageSizeLimit = 10 })
	if r := run(t, ctx, "", nil, "publish", "-s", open, "t", "this message is much too long"); r.err == nil || !strings.Contains(r.err.Error(), "limit of 10 bytes") {
		t.Errorf("err = %v", r.err)
	}
	if r := run(t, ctx, "", nil, "publish", "-s", open, "bad/topic", "x"); r.err == nil || !strings.Contains(r.err.Error(), "invalid topic") {
		t.Errorf("err = %v", r.err)
	}
	if r := run(t, ctx, "", nil, "publish", "-s", open, "-p", "9", "t", "x"); r.err == nil || !strings.Contains(r.err.Error(), "priority") {
		t.Errorf("err = %v", r.err)
	}
	if r := run(t, ctx, "", nil, "publish", "-s", "not a url", "t", "x"); r.err == nil || !strings.Contains(r.err.Error(), "invalid server address") {
		t.Errorf("err = %v", r.err)
	}
	if r := run(t, ctx, "", nil, "publish", "-s", "http://127.0.0.1:1", "t", "x"); r.err == nil {
		t.Error("publishing to nothing succeeded")
	}
	if r := run(t, ctx, "", nil, "publish"); r.err == nil {
		t.Error("publish without a topic succeeded")
	}
}

func TestSubscribePoll(t *testing.T) {
	url := realServer(t)
	ctx := context.Background()
	var first message.Message
	for i, body := range []string{"one", "two", "three"} {
		m := decode(t, run(t, ctx, "", nil, "publish", "-s", url, "a", body).stdout)
		if i == 0 {
			first = m
		}
	}
	run(t, ctx, "", nil, "publish", "-s", url, "b", "other topic")

	r := run(t, ctx, "", nil, "subscribe", "-s", url, "--poll", "a")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if r.err != nil || len(lines) != 3 || decode(t, lines[0]).Message != "one" || decode(t, lines[2]).Message != "three" {
		t.Errorf("poll = %v, %q", r.err, r.stdout)
	}
	r = run(t, ctx, "", nil, "subscribe", "-s", url, "--poll", "--since", first.ID, "a,b")
	lines = strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 3 {
		t.Errorf("poll of two topics after the first message = %q, want 3 lines", r.stdout)
	}
	if r := run(t, ctx, "", nil, "subscribe", "-s", url, "--poll", "--since", "banana", "a"); r.err == nil || !strings.Contains(r.err.Error(), "invalid since") {
		t.Errorf("err = %v", r.err)
	}
}

func TestSubscribeFollowsUntilInterrupted(t *testing.T) {
	url := realServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out syncBuf
	done := make(chan error, 1)
	go func() {
		root := newRootCmd(cli{stdin: strings.NewReader(""), stdout: &out, stderr: &syncBuf{}, getenv: func(string) string { return "" }})
		root.SetArgs([]string{"subscribe", "-s", url, "news"})
		done <- root.ExecuteContext(ctx)
	}()
	time.Sleep(200 * time.Millisecond) // connected
	run(t, context.Background(), "", nil, "publish", "-q", "-s", url, "news", "breaking")

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "breaking") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(out.String(), `"message":"breaking"`) {
		t.Fatalf("output = %q", out.String())
	}
	cancel() // Ctrl-C
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("subscribe returned %v when interrupted, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("subscribe did not stop")
	}
}

type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestVersion(t *testing.T) {
	r := run(t, context.Background(), "", nil, "--version")
	if r.err != nil || !strings.HasPrefix(r.stdout, "flare ") || !strings.HasSuffix(r.stdout, "\n") {
		t.Errorf("--version = %v %q", r.err, r.stdout)
	}
}

// --- serve -------------------------------------------------------------------

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server.yml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServeRefusesBadConfigurationBeforeStarting(t *testing.T) {
	db := "sqlite://" + filepath.Join(t.TempDir(), "x.db")
	tests := []struct {
		name    string
		env     map[string]string
		args    []string
		mention string
	}{
		{"unknown key in the file", nil, []string{"--config", writeConfig(t, "listn: :1\n")}, "listn is not a known setting"},
		{"not supported yet", nil, []string{"--config", writeConfig(t, "push:\n  apps: []\n")}, "push is not a known setting"},
		{"missing file", nil, []string{"--config", filepath.Join(t.TempDir(), "nope.yml")}, "nope.yml"},
		{"bad value from the file", nil, []string{"--config", writeConfig(t, "auth:\n  default-access: everything\n"), "--database", db}, "auth.default-access"},
		{"bad environment", map[string]string{"FLARE_CACHE_DURATION": "soon"}, nil, "FLARE_CACHE_DURATION"},
		{"bad flag value", nil, []string{"--listen", "nowhere", "--database", db}, "listen"},
		{"postgres", nil, []string{"--database", "postgres://x/y"}, "not supported yet"},
		{"bad log level", nil, []string{"--database", db, "--listen", "127.0.0.1:0", "--log-level", "chatty"}, "log-level"},
		{"bad log format", nil, []string{"--database", db, "--listen", "127.0.0.1:0", "--log-format", "xml"}, "log-format"},
		{"config path from the environment", map[string]string{"FLARE_CONFIG": filepath.Join(t.TempDir(), "gone.yml")}, nil, "gone.yml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := run(t, ctx, "", tt.env, append([]string{"serve"}, tt.args...)...)
			if r.err == nil || !strings.Contains(r.err.Error(), tt.mention) {
				t.Errorf("err = %v, want it to mention %q", r.err, tt.mention)
			}
		})
	}
}

func TestServeReportsEveryConfigurationProblemAtOnce(t *testing.T) {
	r := run(t, context.Background(), "", nil, "serve", "--listen", "nowhere", "--database", "mysql://x")
	if r.err == nil || !strings.Contains(r.err.Error(), "listen") || !strings.Contains(r.err.Error(), "database") ||
		!strings.HasPrefix(r.err.Error(), "invalid configuration:") {
		t.Errorf("err = %v", r.err)
	}
}

// serving starts `flare serve` and returns once it answers, with a stop function.
func serving(t *testing.T, env map[string]string, args ...string) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(t, ctx, "", env, append([]string{"serve", "--log-level", "error"}, args...)...).err
	}()
	t.Cleanup(cancel)
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("serve did not stop")
			return nil
		}
	}
}

func reachable(addr string) bool {
	client := http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func waitReachable(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if reachable(addr) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nothing answered on %s", addr)
}

func TestServeStartsAndStops(t *testing.T) {
	addr := freePort(t)
	dir := t.TempDir()
	cfg := writeConfig(t, fmt.Sprintf("listen: %s\ndatabase: sqlite://%s\nbranding:\n  name: Test Server\n", addr, filepath.Join(dir, "f.db")))
	stop := serving(t, nil, "--config", cfg)
	waitReachable(t, addr)

	resp, err := http.Get("http://" + addr + "/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct{ Name string }
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Name != "Test Server" {
		t.Errorf("name = %q: the configuration file was not used", body.Name)
	}
	if err := stop(); err != nil {
		t.Errorf("serve returned %v after a clean shutdown", err)
	}
	if reachable(addr) {
		t.Error("still answering after shutdown")
	}
}

func TestFlagsOverrideTheEnvironmentWhichOverridesTheFile(t *testing.T) {
	fromFile, fromEnv, fromFlag := freePort(t), freePort(t), freePort(t)
	db := "sqlite://" + filepath.Join(t.TempDir(), "f.db")
	cfg := writeConfig(t, fmt.Sprintf("listen: %s\ndatabase: %s\n", fromFile, db))

	// File only.
	stop := serving(t, nil, "--config", cfg)
	waitReachable(t, fromFile)
	stop()

	// The environment beats the file.
	stop = serving(t, map[string]string{"FLARE_LISTEN": fromEnv}, "--config", cfg)
	waitReachable(t, fromEnv)
	if reachable(fromFile) {
		t.Error("the file's address was used although the environment set another")
	}
	stop()

	// A flag beats both.
	stop = serving(t, map[string]string{"FLARE_LISTEN": fromEnv}, "--config", cfg, "--listen", fromFlag)
	waitReachable(t, fromFlag)
	if reachable(fromEnv) || reachable(fromFile) {
		t.Error("an address was used that a flag overrode")
	}
	stop()
}

func TestServeReadsTheConfigPathFromTheEnvironment(t *testing.T) {
	addr := freePort(t)
	cfg := writeConfig(t, fmt.Sprintf("listen: %s\ndatabase: sqlite://%s\n", addr, filepath.Join(t.TempDir(), "f.db")))
	stop := serving(t, map[string]string{"FLARE_CONFIG": cfg})
	waitReachable(t, addr)
	stop()
}

func TestServeFailsWhenThePortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	r := run(t, context.Background(), "", nil, "serve", "--listen", taken.Addr().String(), "--database", "sqlite://"+filepath.Join(t.TempDir(), "f.db"))
	if r.err == nil || !strings.Contains(r.err.Error(), taken.Addr().String()) {
		t.Errorf("err = %v, want it to name the address", r.err)
	}
}

func TestNewLogger(t *testing.T) {
	var b bytes.Buffer
	log, err := newLogger(&b, "json", "warn")
	if err != nil {
		t.Fatal(err)
	}
	log.Info("hidden")
	log.Warn("shown", "k", "v")
	if strings.Contains(b.String(), "hidden") || !strings.Contains(b.String(), `"msg":"shown"`) || !strings.Contains(b.String(), `"k":"v"`) {
		t.Errorf("log output = %q", b.String())
	}
	b.Reset()
	log, _ = newLogger(&b, "text", "debug")
	log.Debug("visible")
	if !strings.Contains(b.String(), "msg=visible") {
		t.Errorf("text log output = %q", b.String())
	}
}
