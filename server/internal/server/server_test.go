package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/message"
)

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

func testConfig(t *testing.T) config.Config {
	cfg := config.Default()
	cfg.Database = "sqlite://" + filepath.Join(t.TempDir(), "flare.db")
	cfg.Listen = "127.0.0.1:0"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// start runs a server on a random port and returns its address and a function
// that stops it and reports what Serve returned.
func start(t *testing.T, cfg config.Config) (url string, srv *Server, stop func() error) {
	t.Helper()
	logs := &syncBuffer{}
	s, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	stopped := false
	stop = func() error {
		cancel()
		select {
		case err := <-done:
			stopped = true
			return err
		case <-time.After(15 * time.Second):
			t.Fatal("Serve did not return after shutdown was requested")
			return nil
		}
	}
	t.Cleanup(func() {
		if !stopped {
			stop()
		}
	})
	return "http://" + ln.Addr().String(), s, stop
}

func TestServesAndShutsDownCleanly(t *testing.T) {
	url, _, stop := start(t, testConfig(t))

	resp, err := http.Post(url+"/t", "text/plain", strings.NewReader("hello"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("publish: %v %v", resp, err)
	}
	resp.Body.Close()
	if err := stop(); err != nil {
		t.Errorf("Serve returned %v after a clean shutdown, want nil", err)
	}
	if _, err := http.Get(url + "/healthz"); err == nil {
		t.Error("the server still accepts connections after shutting down")
	}
}

func TestShutdownEndsOpenStreamsAndWebSocketsPromptly(t *testing.T) {
	url, _, stop := start(t, testConfig(t))

	// A live JSON stream and a WebSocket, both idle.
	resp, err := http.Get(url + "/t/json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	stream := bufio.NewReader(resp.Body)
	if line, _ := stream.ReadString('\n'); !strings.Contains(line, `"event":"open"`) {
		t.Fatalf("first line = %q", line)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http")+"/v1/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	conn.Write(ctx, websocket.MessageText, []byte(`{"op":"subscribe","topic":"t"}`))
	conn.Read(ctx) // subscribed

	begin := time.Now()
	if err := stop(); err != nil {
		t.Errorf("Serve = %v", err)
	}
	if took := time.Since(begin); took > 3*time.Second {
		t.Errorf("shutdown took %v with two idle connections open: they should be ended, not waited for", took)
	}

	// Both clients are told, not left hanging.
	if rest, err := io.ReadAll(stream); err != nil {
		t.Errorf("the JSON stream ended with an error instead of cleanly: %v (%q)", err, rest)
	}
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Errorf("WebSocket read error %v (status %d), want a close with going-away", err, websocket.CloseStatus(err))
	}
}

func TestStopsAcceptingNewRequestsButFinishesTheOnesInProgress(t *testing.T) {
	url, _, stop := start(t, testConfig(t))
	// A publish whose body arrives slowly is "in progress" when shutdown starts.
	pr, pw := io.Pipe()
	req, _ := http.NewRequest("POST", url+"/t", pr)
	result := make(chan *http.Response, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
		}
		result <- resp
	}()
	pw.Write([]byte("part one, "))
	time.Sleep(100 * time.Millisecond)

	go func() { time.Sleep(200 * time.Millisecond); pw.Write([]byte("and part two")); pw.Close() }()
	if err := stop(); err != nil {
		t.Errorf("Serve = %v", err)
	}
	resp := <-result
	if resp == nil || resp.StatusCode != 200 {
		t.Fatalf("the publish in progress was not completed: %v", resp)
	}
	var m message.Message
	json.NewDecoder(resp.Body).Decode(&m)
	if m.Message != "part one, and part two" {
		t.Errorf("message = %q", m.Message)
	}
}

func TestExpiredMessagesArePurgedInTheBackground(t *testing.T) {
	cfg := testConfig(t)
	s, err := New(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	s.purgeEvery = 10 * time.Millisecond

	now := time.Now()
	old := message.Message{ID: "00000000-0001-7000-8000-000000000000", Time: now.Add(-48 * time.Hour).Unix(),
		Expires: now.Add(-24 * time.Hour).Unix(), Event: "message", Topic: "t", Message: "old", Priority: 3}
	if err := s.store.Insert(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	// Looking at the store as it was a long time ago, an unpurged row is visible.
	visible := func() int {
		got, _ := s.store.After(context.Background(), []string{"t"}, "", time.Unix(1, 0), 10)
		return len(got)
	}
	if visible() != 1 {
		t.Fatal("test setup: the old message should be in the store")
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(5 * time.Second)
	for visible() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if visible() != 0 {
		t.Error("the expired message was never purged")
	}
}

func TestStoreIsClosedAfterShutdown(t *testing.T) {
	_, s, stop := start(t, testConfig(t))
	stop()
	if err := s.store.Ping(context.Background()); err == nil {
		t.Error("the store is still open after shutdown")
	}
}

func TestNewFailsClearlyWhenTheDatabaseCannotBeOpened(t *testing.T) {
	cfg := testConfig(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Database = "sqlite://" + filepath.Join(blocker, "flare.db")
	_, err := New(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), "message store") {
		t.Errorf("err = %v, want a clear error about the message store", err)
	}
}

func TestRunReportsAPortThatIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	cfg := testConfig(t)
	cfg.Listen = taken.Addr().String()
	err = Run(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), cfg.Listen) {
		t.Errorf("err = %v, want it to name the address %s", err, cfg.Listen)
	}
}

func TestOpenAccessIsWarnedAboutUnlessItCannotBeReachedFromOutside(t *testing.T) {
	for _, tt := range []struct {
		listen, access string
		warns          bool
	}{
		{":0", "read-write", true},
		{"0.0.0.0:0", "read-write", true},
		{":0", "read-only", true},
		{":0", "deny-all", false},
		{"127.0.0.1:0", "read-write", false},
		{"localhost:0", "read-write", false},
		{"[::1]:0", "read-write", false},
	} {
		t.Run(tt.listen+"/"+tt.access, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.Listen, cfg.Auth.DefaultAccess = tt.listen, tt.access
			logs := &syncBuffer{}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- Run(ctx, cfg, slog.New(slog.NewTextHandler(logs, nil))) }()
			deadline := time.Now().Add(5 * time.Second)
			for !strings.Contains(logs.String(), "listening") && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			<-done
			if got := strings.Contains(logs.String(), "this server is open"); got != tt.warns {
				t.Errorf("warned = %v, want %v:\n%s", got, tt.warns, logs.String())
			}
		})
	}
}

func TestLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true, "localhost:8080": true, "[::1]:8080": true, "127.1.2.3:1": true,
		":8080": false, "0.0.0.0:8080": false, "[::]:8080": false, "192.168.1.5:8080": false, "example.com:80": false, "garbage": false,
	} {
		if got := loopback(addr); got != want {
			t.Errorf("loopback(%q) = %v, want %v", addr, got, want)
		}
	}
}
