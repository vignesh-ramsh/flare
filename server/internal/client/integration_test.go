package client_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/client"
	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/server"
)

// realServer runs the actual server; unavailable, when set, makes it refuse
// stream requests with a 503, which is what a restarting server looks like.
func realServer(t *testing.T) (c *client.Client, srv *httptest.Server, unavailable *atomic.Bool) {
	t.Helper()
	cfg := config.Default()
	cfg.Database = "sqlite://" + filepath.Join(t.TempDir(), "flare.db")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	s, err := server.New(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	unavailable = new(atomic.Bool)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() && r.Method == "GET" {
			http.Error(w, "restarting", http.StatusServiceUnavailable)
			return
		}
		s.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	c, err = client.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c.MinBackoff, c.MaxBackoff = 5*time.Millisecond, 20*time.Millisecond
	return c, srv, unavailable
}

type feed struct {
	mu   sync.Mutex
	got  []string
	stop context.CancelFunc
	done chan error
}

func follow(t *testing.T, c *client.Client, opt client.SubscribeOptions) *feed {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	f := &feed{stop: cancel, done: make(chan error, 1)}
	t.Cleanup(cancel)
	go func() {
		f.done <- c.Subscribe(ctx, []string{"news"}, opt, func(m message.Message) error {
			f.mu.Lock()
			f.got = append(f.got, m.Message)
			f.mu.Unlock()
			return nil
		})
	}()
	return f
}

func (f *feed) waitFor(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := strings.Join(f.got, ",")
		f.mu.Unlock()
		if got == strings.Join(want, ",") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("got %v, want %v", f.got, want)
}

func publish(t *testing.T, c *client.Client, body string) message.Message {
	t.Helper()
	m, err := c.Publish(context.Background(), "news", body, client.PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPublishAndPollAgainstTheRealServer(t *testing.T) {
	c, _, _ := realServer(t)
	a := publish(t, c, "first")
	publish(t, c, "second")

	var got []string
	err := c.Subscribe(context.Background(), []string{"news"}, client.SubscribeOptions{Poll: true}, func(m message.Message) error {
		got = append(got, m.Message)
		return nil
	})
	if err != nil || strings.Join(got, ",") != "first,second" {
		t.Errorf("poll = %v, %v", got, err)
	}
	got = nil
	c.Subscribe(context.Background(), []string{"news"}, client.SubscribeOptions{Poll: true, Since: a.ID}, func(m message.Message) error {
		got = append(got, m.Message)
		return nil
	})
	if strings.Join(got, ",") != "second" {
		t.Errorf("poll since the first = %v", got)
	}
}

func TestBacklogThenLiveMessagesArriveInOrder(t *testing.T) {
	c, _, _ := realServer(t)
	for i := range 5 {
		publish(t, c, fmt.Sprint("old", i))
	}
	f := follow(t, c, client.SubscribeOptions{Since: "all"})
	f.waitFor(t, "old0", "old1", "old2", "old3", "old4")
	for i := range 5 {
		publish(t, c, fmt.Sprint("new", i))
	}
	f.waitFor(t, "old0", "old1", "old2", "old3", "old4", "new0", "new1", "new2", "new3", "new4")
}

func TestAMessagePublishedWhileTheConnectionWasDownIsNotLost(t *testing.T) {
	c, srv, unavailable := realServer(t)
	f := follow(t, c, client.SubscribeOptions{}) // new messages only
	time.Sleep(100 * time.Millisecond)           // let it connect and receive the open event
	publish(t, c, "before")
	f.waitFor(t, "before")

	// The connection drops, and the server stays unreachable for a stretch...
	unavailable.Store(true)
	srv.CloseClientConnections()
	time.Sleep(50 * time.Millisecond)
	// ...during which messages are published (by someone who can still reach it).
	publish(t, c, "during 1")
	publish(t, c, "during 2")
	time.Sleep(50 * time.Millisecond)
	unavailable.Store(false)

	f.waitFor(t, "before", "during 1", "during 2")
	publish(t, c, "after")
	f.waitFor(t, "before", "during 1", "during 2", "after")
}

func TestALiveSubscriberThatHadNoMessagesYetStillCatchesTheOnesDuringAnOutage(t *testing.T) {
	// The case the open event's ID exists for: nothing has arrived, so there is no
	// message ID to resume from.
	c, srv, unavailable := realServer(t)
	f := follow(t, c, client.SubscribeOptions{})
	time.Sleep(100 * time.Millisecond)

	unavailable.Store(true)
	srv.CloseClientConnections()
	time.Sleep(50 * time.Millisecond)
	publish(t, c, "published during the outage")
	unavailable.Store(false)

	f.waitFor(t, "published during the outage")
}

func TestSubscribeEndsCleanlyWhenCancelled(t *testing.T) {
	c, _, _ := realServer(t)
	f := follow(t, c, client.SubscribeOptions{})
	time.Sleep(50 * time.Millisecond)
	f.stop()
	select {
	case err := <-f.done:
		if err != nil {
			t.Errorf("Subscribe = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe did not return")
	}
}
