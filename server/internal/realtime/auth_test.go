package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
)

func someone(name string) auth.Identity {
	return auth.Identity{User: &auth.User{ID: "id-" + name, Name: name, Role: auth.RoleUser}}
}

// who records which identity each authorization was asked about.
type who struct {
	mu    sync.Mutex
	names []string
}

func (w *who) authorize(_ context.Context, id auth.Identity, _ string, _ auth.Permission) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	name := "anonymous"
	if id.User != nil {
		name = id.User.Name
	}
	w.names = append(w.names, name)
	return nil
}

func (w *who) last() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.names) == 0 {
		return ""
	}
	return w.names[len(w.names)-1]
}

func TestWebSocketAuthorizesAsTheIdentityTheConnectionWasOpenedWith(t *testing.T) {
	h, _ := newHub(t, Options{})
	var seen who
	srv := wsServer(t, h, WSOptions{Identity: someone("alice"), Authorize: seen.authorize})
	c := dialWS(t, srv.URL)
	c.send(map[string]any{"op": "subscribe", "topic": "t"})
	c.expect("subscribed")
	c.send(map[string]any{"op": "publish", "topic": "t", "message": "x"})
	c.expectBoth("published", "message") // it follows the topic it published to
	if got := seen.last(); got != "alice" {
		t.Errorf("authorized as %q, want alice", got)
	}
}

func TestWebSocketLoginFrame(t *testing.T) {
	h, _ := newHub(t, Options{})
	var seen who
	login := func(_ context.Context, token string) (auth.Identity, error) {
		switch token {
		case "good":
			return someone("bob"), nil
		case "bad":
			return auth.Identity{}, auth.ErrBadCredentials
		case "slow-down":
			return auth.Identity{}, &auth.ThrottledError{RetryAfter: 90 * time.Second}
		}
		return auth.Identity{}, errors.New("sql: database is locked")
	}
	srv := wsServer(t, h, WSOptions{Authorize: seen.authorize, Login: login})
	c := dialWS(t, srv.URL)

	c.send(map[string]any{"op": "auth", "token": "bad"})
	c.expectError(apierr.InvalidCredentials, "not valid")
	c.send(map[string]any{"op": "auth", "token": "slow-down"})
	c.expectError(apierr.Throttled, "1m30s")
	c.send(map[string]any{"op": "auth", "token": "???"})
	c.expectError(apierr.Internal, "internal error") // and nothing about the database
	c.send(map[string]any{"op": "publish", "topic": "t", "message": "x"})
	c.expect("published")
	if got := seen.last(); got != "anonymous" {
		t.Errorf("after failed logins the connection acts as %q, want anonymous", got)
	}

	c.send(map[string]any{"op": "auth", "token": "good"})
	if f := c.expect("authenticated"); f["user"] != "bob" {
		t.Errorf("authenticated frame = %v", f)
	}
	c.send(map[string]any{"op": "subscribe", "topic": "t"})
	c.expect("subscribed")
	if got := seen.last(); got != "bob" {
		t.Errorf("authorized as %q after logging in, want bob", got)
	}
}

func TestWebSocketLoginIsRefusedOnceSubscribed(t *testing.T) {
	// Otherwise a subscription authorized as one person could outlive a switch to
	// another.
	h, _ := newHub(t, Options{})
	srv := wsServer(t, h, WSOptions{Login: func(context.Context, string) (auth.Identity, error) { return someone("bob"), nil }})
	c := dialWS(t, srv.URL)
	c.send(map[string]any{"op": "subscribe", "topic": "t"})
	c.expect("subscribed")
	c.send(map[string]any{"op": "auth", "token": "good"})
	c.expectError(apierr.InvalidParameter, "before subscribing")
}

func TestWebSocketWithoutAccountsRefusesToLogIn(t *testing.T) {
	h, _ := newHub(t, Options{})
	srv := wsServer(t, h, WSOptions{})
	c := dialWS(t, srv.URL)
	c.send(map[string]any{"op": "auth", "token": "anything"})
	c.expectError(apierr.Unauthorized, "no accounts")
}

func TestWebSocketReportsWhyAnOperationWasRefused(t *testing.T) {
	h, _ := newHub(t, Options{})
	srv := wsServer(t, h, WSOptions{Authorize: func(_ context.Context, id auth.Identity, topic string, _ auth.Permission) error {
		switch topic {
		case "login-first":
			return auth.ErrUnauthorized
		case "no":
			return auth.ErrForbidden
		}
		return errors.New("sql: database is locked")
	}})
	c := dialWS(t, srv.URL)
	c.send(map[string]any{"op": "subscribe", "topic": "login-first"})
	c.expectError(apierr.Unauthorized, "unauthorized")
	c.send(map[string]any{"op": "subscribe", "topic": "no"})
	c.expectError(apierr.Forbidden, "forbidden")
	c.send(map[string]any{"op": "publish", "topic": "broken", "message": "x"})
	c.expectError(apierr.Internal, "internal error")
}

func TestWebSocketIsClosedWhenItsCredentialsAreRevoked(t *testing.T) {
	h, _ := newHub(t, Options{})
	var revoked atomic.Bool
	srv := wsServer(t, h, WSOptions{
		Identity:     someone("alice"),
		PingInterval: 20 * time.Millisecond,
		Revalidate: func(_ context.Context, id auth.Identity) (auth.Identity, error) {
			if revoked.Load() {
				return auth.Identity{}, auth.ErrBadCredentials
			}
			return id, nil
		},
	})
	c := dialWS(t, srv.URL)
	c.send(map[string]any{"op": "subscribe", "topic": "t"})
	c.expect("subscribed")
	time.Sleep(60 * time.Millisecond) // several revalidations pass while all is well
	mustPublish(t, h, "t", "still allowed")
	c.expect("message")

	revoked.Store(true)
	c.expectError(apierr.InvalidCredentials, "no longer valid")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Errorf("read error %v (status %d), want the connection closed as a policy violation", err, websocket.CloseStatus(err))
	}
}

func TestWebSocketLosesOnlyTheSubscriptionsItIsNoLongerAllowed(t *testing.T) {
	h, _ := newHub(t, Options{})
	var withdrawn atomic.Bool
	srv := wsServer(t, h, WSOptions{
		Identity:     someone("alice"),
		PingInterval: 20 * time.Millisecond,
		Authorize: func(_ context.Context, _ auth.Identity, topic string, _ auth.Permission) error {
			if topic == "b" && withdrawn.Load() {
				return auth.ErrForbidden
			}
			return nil
		},
		Revalidate: func(_ context.Context, id auth.Identity) (auth.Identity, error) { return id, nil },
	})
	c := dialWS(t, srv.URL)
	for _, topic := range []string{"a", "b"} {
		c.send(map[string]any{"op": "subscribe", "topic": topic})
		c.expect("subscribed")
	}

	withdrawn.Store(true)
	c.expectError(apierr.Forbidden, "forbidden") // for b
	time.Sleep(50 * time.Millisecond)
	mustPublish(t, h, "b", "no longer delivered")
	kept := mustPublish(t, h, "a", "still delivered")
	if f := c.expect("message"); f["id"] != kept.ID {
		t.Errorf("got %v: topic b should have been dropped and topic a kept", f)
	}
}

func TestWebSocketIgnoresAFailureToRevalidateThatIsNotTheCredentials(t *testing.T) {
	h, _ := newHub(t, Options{})
	var calls atomic.Int32
	srv := wsServer(t, h, WSOptions{
		PingInterval: 15 * time.Millisecond,
		Revalidate: func(_ context.Context, id auth.Identity) (auth.Identity, error) {
			calls.Add(1)
			return auth.Identity{}, errors.New("sql: database is locked") // a hiccup, not a revocation
		},
	})
	c := dialWS(t, srv.URL)
	c.send(map[string]any{"op": "subscribe", "topic": "t"})
	c.expect("subscribed")

	// A client has to be reading to answer the server's pings, as a browser
	// always is, so read in the background.
	frames := make(chan map[string]any, 16)
	go func() {
		for {
			_, data, err := c.conn.Read(context.Background())
			if err != nil {
				close(frames)
				return
			}
			var f map[string]any
			json.Unmarshal(data, &f)
			frames <- f
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() < 3 {
		t.Fatalf("revalidated only %d times", calls.Load())
	}
	m := mustPublish(t, h, "t", "the connection survived")
	select {
	case f, ok := <-frames:
		if !ok || f["id"] != m.ID {
			t.Errorf("got %v (open: %v), want the message: the connection should have survived", f, ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message arrived")
	}
}

func TestStreamsEndWhenAccessIsWithdrawn(t *testing.T) {
	for name, serve := range map[string]func(http.ResponseWriter, *http.Request, *Hub, StreamOptions) error{
		"json": func(w http.ResponseWriter, r *http.Request, h *Hub, o StreamOptions) error {
			return ServeJSON(w, r, h, []string{"t"}, LiveCursor, o)
		},
		"sse": func(w http.ResponseWriter, r *http.Request, h *Hub, o StreamOptions) error {
			return ServeSSE(w, r, h, []string{"t"}, LiveCursor, o)
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := newHub(t, Options{})
			var checks atomic.Int32
			result := make(chan error, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result <- serve(w, r, h, StreamOptions{
					Heartbeat: 15 * time.Millisecond,
					Revalidate: func() error {
						if checks.Add(1) >= 4 {
							return auth.ErrForbidden
						}
						return nil
					},
				})
			}))
			defer srv.Close()

			resp, err := http.Get(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			select {
			case err := <-result:
				var revoked *RevokedError
				if !errors.As(err, &revoked) || !errors.Is(err, auth.ErrForbidden) {
					t.Errorf("stream ended with %v, want a RevokedError wrapping the reason", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the stream kept running after access was withdrawn")
			}
		})
	}
}
