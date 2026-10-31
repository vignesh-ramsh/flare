package realtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/broker"
	"github.com/vignesh-ramsh/flare/server/internal/id"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Unix(1_800_000_000, 0)} }
func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newHub(t *testing.T, opt Options) (*Hub, *broker.InProc) {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "flare.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	br := broker.NewInProc()
	return NewHub(st, br, opt), br
}

func draft(topic, body string) message.Draft { return message.Draft{Topic: topic, Body: body} }

func mustPublish(t *testing.T, h *Hub, topic, body string) message.Message {
	t.Helper()
	m, err := h.Publish(context.Background(), draft(topic, body))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func ids(ms []message.Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func replay(t *testing.T, h *Hub, topics []string, after string) []message.Message {
	t.Helper()
	var out []message.Message
	err := h.Replay(context.Background(), topics, after, func(m message.Message) error {
		out = append(out, m)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPublishStoresAndAssignsIncreasingIDs(t *testing.T) {
	clk := newClock()
	h, _ := newHub(t, Options{TTL: time.Hour, Now: clk.Now})
	d := message.Draft{Topic: "t", Title: "Hello", Body: "body", Priority: 4, Tags: []string{"a"}, Click: "https://example.com"}
	m1, err := h.Publish(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	m2 := mustPublish(t, h, "t", "second")

	if !id.Valid(m1.ID) || m2.ID <= m1.ID {
		t.Errorf("IDs %q, %q should be valid and increasing", m1.ID, m2.ID)
	}
	want := message.Message{ID: m1.ID, Time: clk.Now().Unix(), Expires: clk.Now().Add(time.Hour).Unix(),
		Event: "message", Topic: "t", Title: "Hello", Message: "body", Priority: 4, Tags: []string{"a"}, Click: "https://example.com"}
	if !reflect.DeepEqual(m1, want) {
		t.Errorf("published message:\n got  %+v\n want %+v", m1, want)
	}
	if got := replay(t, h, []string{"t"}, ""); !reflect.DeepEqual(got, []message.Message{m1, m2}) {
		t.Errorf("stored messages = %+v", got)
	}
}

func TestPublishAppliesDefaults(t *testing.T) {
	h, _ := newHub(t, Options{})
	m := mustPublish(t, h, "t", "")
	if m.Message != "triggered" || m.Priority != message.DefaultPriority {
		t.Errorf("got %+v, want the placeholder body and default priority", m)
	}
}

func TestRefusedDraftIsNeitherStoredNorDelivered(t *testing.T) {
	h, br := newHub(t, Options{MaxMessageSize: 10})
	sub := br.Subscribe([]string{"t"}, 10)
	defer sub.Close()

	_, err := h.Publish(context.Background(), draft("t", "this is longer than ten bytes"))
	var p *message.Problem
	if !errors.As(err, &p) || !p.TooLarge {
		t.Fatalf("err = %v, want a too-large problem", err)
	}
	_, err = h.Publish(context.Background(), draft("bad/topic", "x"))
	if !errors.As(err, &p) || p.Field != "topic" {
		t.Fatalf("err = %v, want a topic problem", err)
	}
	if got := replay(t, h, []string{"t", "bad/topic"}, ""); len(got) != 0 {
		t.Errorf("stored %d refused messages", len(got))
	}
	select {
	case m := <-sub.C():
		t.Errorf("a refused message was delivered: %+v", m)
	case <-time.After(30 * time.Millisecond):
	}
}

type failingStore struct{ store.Store }

func (failingStore) Insert(context.Context, message.Message) error { return errors.New("disk full") }

func TestAMessageThatCouldNotBeStoredIsNotDelivered(t *testing.T) {
	br := broker.NewInProc()
	h := NewHub(failingStore{}, br, Options{})
	sub := br.Subscribe([]string{"t"}, 10)
	defer sub.Close()

	if _, err := h.Publish(context.Background(), draft("t", "x")); err == nil {
		t.Fatal("Publish succeeded although storing failed")
	}
	select {
	case m := <-sub.C():
		t.Errorf("delivered a message that was never stored: %+v", m)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestExpiredMessagesAreNotReplayed(t *testing.T) {
	clk := newClock()
	h, _ := newHub(t, Options{TTL: time.Hour, Now: clk.Now})
	mustPublish(t, h, "t", "x")
	clk.Advance(59 * time.Minute)
	if got := replay(t, h, []string{"t"}, ""); len(got) != 1 {
		t.Errorf("after 59m got %d messages, want 1", len(got))
	}
	clk.Advance(2 * time.Minute)
	if got := replay(t, h, []string{"t"}, ""); len(got) != 0 {
		t.Errorf("after 61m got %d messages, want 0", len(got))
	}
}

func TestCheckpointSortsBetweenOldAndNewMessages(t *testing.T) {
	clk := newClock()
	h, _ := newHub(t, Options{Now: clk.Now})
	before := mustPublish(t, h, "t", "before")
	clk.Advance(time.Second)
	cp := h.Checkpoint()
	clk.Advance(time.Second)
	after := mustPublish(t, h, "t", "after")
	if !(before.ID < cp && cp < after.ID) {
		t.Errorf("want %s < %s < %s", before.ID, cp, after.ID)
	}
}

func TestReplayPagesThroughALargeBacklog(t *testing.T) {
	h, _ := newHub(t, Options{PageSize: 3})
	var want []string
	for i := range 10 {
		want = append(want, mustPublish(t, h, "t", fmt.Sprint(i)).ID)
	}
	if got := ids(replay(t, h, []string{"t"}, "")); !reflect.DeepEqual(got, want) {
		t.Errorf("replayed %v, want %v", got, want)
	}
	if got := ids(replay(t, h, []string{"t"}, want[6])); !reflect.DeepEqual(got, want[7:]) {
		t.Errorf("replay after the 7th message = %v, want %v", got, want[7:])
	}
}

func TestReplayStopsWhenTheHandlerFails(t *testing.T) {
	h, _ := newHub(t, Options{})
	mustPublish(t, h, "t", "1")
	mustPublish(t, h, "t", "2")
	boom := errors.New("client went away")
	calls := 0
	err := h.Replay(context.Background(), []string{"t"}, "", func(message.Message) error { calls++; return boom })
	if err != boom || calls != 1 {
		t.Errorf("err = %v after %d calls, want the handler's error after 1", err, calls)
	}
}

// follow runs Follow in the background, collecting what it delivers.
type follower struct {
	mu     sync.Mutex
	got    []message.Message
	opened chan struct{}
	done   chan error
	cancel context.CancelFunc
}

func startFollow(h *Hub, topics []string, c Cursor, onMessage func(message.Message) error) *follower {
	ctx, cancel := context.WithCancel(context.Background())
	f := &follower{opened: make(chan struct{}), done: make(chan error, 1), cancel: cancel}
	go func() {
		f.done <- h.Follow(ctx, topics, c, 0, Handler{
			Open: func() error { close(f.opened); return nil },
			Message: func(m message.Message) error {
				f.mu.Lock()
				f.got = append(f.got, m)
				f.mu.Unlock()
				if onMessage != nil {
					return onMessage(m)
				}
				return nil
			},
		})
	}()
	return f
}

func (f *follower) messages() []message.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]message.Message(nil), f.got...)
}

func (f *follower) waitFor(t *testing.T, n int) []message.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := f.messages(); len(got) >= n {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out: received %d of %d messages", len(f.messages()), n)
	return nil
}

func (f *follower) stop(t *testing.T) error {
	t.Helper()
	f.cancel()
	select {
	case err := <-f.done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Follow did not return after its context was cancelled")
		return nil
	}
}

func TestFollowDeliversBacklogThenLiveMessagesInOrder(t *testing.T) {
	h, _ := newHub(t, Options{})
	a := mustPublish(t, h, "t", "backlog 1")
	b := mustPublish(t, h, "t", "backlog 2")
	f := startFollow(h, []string{"t"}, AllCursor, nil)
	<-f.opened
	c := mustPublish(t, h, "t", "live 1")
	d := mustPublish(t, h, "t", "live 2")

	got := f.waitFor(t, 4)
	if want := []string{a.ID, b.ID, c.ID, d.ID}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("got %v, want %v", ids(got), want)
	}
	if err := f.stop(t); err != nil {
		t.Errorf("Follow returned %v after cancellation, want nil", err)
	}
}

func TestFollowFromAnIDSkipsEarlierMessages(t *testing.T) {
	h, _ := newHub(t, Options{})
	a := mustPublish(t, h, "t", "a")
	b := mustPublish(t, h, "t", "b")
	f := startFollow(h, []string{"t"}, Cursor{After: a.ID}, nil)
	<-f.opened
	if got := f.waitFor(t, 1); got[0].ID != b.ID {
		t.Errorf("first message = %s, want %s", got[0].ID, b.ID)
	}
	f.stop(t)
}

func TestLiveCursorSkipsTheBacklog(t *testing.T) {
	h, _ := newHub(t, Options{})
	mustPublish(t, h, "t", "old")
	f := startFollow(h, []string{"t"}, LiveCursor, nil)
	<-f.opened
	fresh := mustPublish(t, h, "t", "new")
	got := f.waitFor(t, 1)
	time.Sleep(20 * time.Millisecond)
	if got = f.messages(); len(got) != 1 || got[0].ID != fresh.ID {
		t.Errorf("got %v, want only the new message", ids(got))
	}
	f.stop(t)
}

func TestFollowOnlySeesItsOwnTopics(t *testing.T) {
	h, _ := newHub(t, Options{})
	mustPublish(t, h, "other", "no")
	f := startFollow(h, []string{"a", "b"}, AllCursor, nil)
	<-f.opened
	x := mustPublish(t, h, "a", "yes")
	mustPublish(t, h, "other", "no")
	y := mustPublish(t, h, "b", "yes")
	got := f.waitFor(t, 2)
	time.Sleep(20 * time.Millisecond)
	if got = f.messages(); !reflect.DeepEqual(ids(got), []string{x.ID, y.ID}) {
		t.Errorf("got %v, want %v", ids(got), []string{x.ID, y.ID})
	}
	f.stop(t)
}

func TestOpenMeansNothingPublishedFromNowOnIsMissed(t *testing.T) {
	// Publishing from inside Open happens "right after subscribing". A
	// live-only subscriber must still receive it.
	h, _ := newHub(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var published message.Message
	var got []message.Message
	err := h.Follow(ctx, []string{"t"}, LiveCursor, 0, Handler{
		Open: func() error {
			var err error
			published, err = h.Publish(ctx, draft("t", "right after open"))
			return err
		},
		Message: func(m message.Message) error { got = append(got, m); cancel(); return nil },
	})
	if err != nil || len(got) != 1 || got[0].ID != published.ID {
		t.Errorf("err = %v, got %v; want the message published in Open", err, ids(got))
	}
}

func TestFollowReturnsHandlerErrors(t *testing.T) {
	h, _ := newHub(t, Options{})
	boom := errors.New("write failed")

	err := h.Follow(context.Background(), []string{"t"}, LiveCursor, 0, Handler{
		Open:    func() error { return boom },
		Message: func(message.Message) error { t.Error("delivered after Open failed"); return nil },
	})
	if err != boom {
		t.Errorf("Open error: got %v", err)
	}

	mustPublish(t, h, "t", "x")
	err = h.Follow(context.Background(), []string{"t"}, AllCursor, 0, Handler{
		Message: func(message.Message) error { return boom },
	})
	if err != boom {
		t.Errorf("backlog Message error: got %v", err)
	}

	f := startFollow(h, []string{"t"}, LiveCursor, func(message.Message) error { return boom })
	<-f.opened
	mustPublish(t, h, "t", "y")
	select {
	case err := <-f.done:
		if err != boom {
			t.Errorf("live Message error: got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Follow kept running after the handler failed")
	}
}

func TestHeartbeats(t *testing.T) {
	h, _ := newHub(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	beats := 0
	err := h.Follow(ctx, []string{"t"}, LiveCursor, 5*time.Millisecond, Handler{
		Message: func(message.Message) error { return nil },
		Heartbeat: func() error {
			if beats++; beats == 3 {
				cancel()
			}
			return nil
		},
	})
	if err != nil || beats != 3 {
		t.Errorf("err = %v after %d heartbeats, want nil after 3", err, beats)
	}

	boom := errors.New("connection gone")
	err = h.Follow(context.Background(), []string{"t"}, LiveCursor, 5*time.Millisecond, Handler{
		Message:   func(message.Message) error { return nil },
		Heartbeat: func() error { return boom },
	})
	if err != boom {
		t.Errorf("a failing heartbeat should end Follow with its error, got %v", err)
	}
}

func TestSlowSubscriberIsDroppedAndCanResumeWithoutLoss(t *testing.T) {
	h, _ := newHub(t, Options{Buffer: 2})
	const total = 25

	var (
		seen     []string
		lagged   int
		firstGet = make(chan struct{})
		gate     = make(chan struct{})
		once     sync.Once
		ctx      = context.Background()
		cursor   = AllCursor
		want     []string
	)

	go func() { // publish the rest while the subscriber is stuck on its first message
		<-firstGet
		for i := 1; i < total; i++ {
			want = append(want, mustPublish(t, h, "t", fmt.Sprint(i)).ID)
		}
		close(gate)
	}()

	first := make(chan message.Message, 1)
	go func() {
		m, err := h.Publish(ctx, draft("t", "0"))
		if err != nil {
			t.Error(err)
		}
		first <- m
	}()
	m0 := <-first
	want = append([]string{m0.ID}, want...)

	// Resume loop: what a client does when its connection is dropped.
	for attempt := 0; len(seen) < total; attempt++ {
		if attempt > 20 {
			t.Fatalf("still missing messages after %d reconnects: have %d of %d", attempt, len(seen), total)
		}
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := h.Follow(runCtx, []string{"t"}, cursor, 0, Handler{
			Message: func(m message.Message) error {
				seen = append(seen, m.ID)
				cursor = Cursor{After: m.ID}
				once.Do(func() { close(firstGet); <-gate }) // block on the very first delivery
				if len(seen) == total {
					cancel()
				}
				return nil
			},
		})
		timedOut := runCtx.Err() == context.DeadlineExceeded
		cancel()
		if timedOut {
			t.Fatalf("timed out with %d of %d messages: a message was never delivered", len(seen), total)
		}
		if errors.Is(err, broker.ErrLagged) {
			lagged++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if lagged == 0 {
		t.Error("the slow subscriber was never dropped, so this test did not exercise resuming")
	}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("after resuming, saw %d messages in the wrong order or with gaps or repeats\n got  %v\n want %v", len(seen), seen, want)
	}
}

// The property the whole design exists for: while messages are being published
// from many goroutines, a subscriber that connects at any moment receives every
// message after its cursor exactly once, in ID order.
func TestSubscriberConnectingDuringPublishingNeverMissesOrRepeats(t *testing.T) {
	const publishers, perPublisher = 4, 40
	topics := []string{"a", "b"}

	for iteration := range 15 {
		for _, mode := range []string{"all", "after-id", "live"} {
			t.Run(fmt.Sprintf("%s/%d", mode, iteration), func(t *testing.T) {
				h, _ := newHub(t, Options{PageSize: 5, Buffer: 100_000})

				var (
					mu        sync.Mutex
					published []message.Message
					wg        sync.WaitGroup
				)
				publish := func(topic, body string) message.Message {
					m := mustPublish(t, h, topic, body)
					mu.Lock()
					published = append(published, m)
					mu.Unlock()
					return m
				}
				preloaded := publish("a", "preloaded 1")
				before := map[string]bool{preloaded.ID: true, publish("b", "preloaded 2").ID: true}

				cursor := map[string]Cursor{"all": AllCursor, "after-id": {After: preloaded.ID}, "live": LiveCursor}[mode]
				f := startFollow(h, topics, cursor, nil)

				if mode == "live" {
					<-f.opened // everything published from here on must arrive
				}

				for p := range publishers {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for i := range perPublisher {
							topic := []string{"a", "b", "c"}[(p+i)%3] // "c" is not followed
							publish(topic, fmt.Sprintf("%d-%d", p, i))
							if i == perPublisher/2 && mode != "live" {
								<-f.opened // the follower is attaching somewhere in the middle
							}
						}
					}()
				}
				wg.Wait()

				var want []string
				mu.Lock()
				for _, m := range published {
					if m.Topic == "c" {
						continue
					}
					// An ID comparison would be wrong for "live": IDs only resolve to a
					// millisecond, so a message published just before subscribing can
					// still sort after any ID taken just after.
					if (mode == "after-id" && m.ID <= preloaded.ID) || (mode == "live" && before[m.ID]) {
						continue
					}
					want = append(want, m.ID)
				}
				mu.Unlock()
				sort.Strings(want)

				got := f.waitFor(t, len(want))
				time.Sleep(10 * time.Millisecond) // give any wrongly repeated message time to arrive
				got = f.messages()
				f.stop(t)

				if !reflect.DeepEqual(ids(got), want) {
					seen := map[string]int{}
					for _, m := range got {
						seen[m.ID]++
					}
					dups := 0
					for _, n := range seen {
						if n > 1 {
							dups++
						}
					}
					t.Fatalf("received %d messages, want %d (%d repeated); order sorted: %v",
						len(got), len(want), dups, sort.StringsAreSorted(ids(got)))
				}
			})
		}
	}
}

func TestParseCursor(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	someID := "01933c3e-7a4f-7cc1-9b2e-5f1d6a8e4c21"
	good := map[string]Cursor{
		"all":        AllCursor,
		someID:       {After: someID},
		"10m":        {After: id.Boundary(now.Add(-10 * time.Minute))},
		"2h":         {After: id.Boundary(now.Add(-2 * time.Hour))},
		"1.5h":       {After: id.Boundary(now.Add(-90 * time.Minute))},
		"1799999000": {After: id.Boundary(time.Unix(1_799_999_000, 0))},
		"1000000000": {After: id.Boundary(time.Unix(1_000_000_000, 0))},
		"876000h":    AllCursor, // 100 years back reaches before the epoch
	}
	for in, want := range good {
		got, err := ParseCursor(in, now)
		if err != nil || got != want {
			t.Errorf("ParseCursor(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	// Bare small numbers are ambiguous (seconds, or a date in 1970?), so they fail too.
	for _, bad := range []string{"", "banana", "-5m", "0s", "5x", "01933C3E-7A4F-7CC1-9B2E-5F1D6A8E4C21", "99999999999999999999", "all ",
		"0", "10", "30", "999999999"} {
		if c, err := ParseCursor(bad, now); err == nil {
			t.Errorf("ParseCursor(%q) = %+v, want an error", bad, c)
		}
	}
}

func TestMessageFindsAStoredMessageUntilItExpires(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	h, _ := newHub(t, Options{TTL: time.Hour, Now: func() time.Time { return now }})
	m := mustPublish(t, h, "t", "hello")

	got, err := h.Message(context.Background(), m.ID)
	if err != nil || !reflect.DeepEqual(got, m) {
		t.Errorf("Message = %+v, %v; want %+v", got, err, m)
	}
	if _, err := h.Message(context.Background(), "unknown"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown ID: %v", err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := h.Message(context.Background(), m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expired: %v", err)
	}
}

func TestStoredIsCalledOncePerStoredMessageAndNotForRefusedOnes(t *testing.T) {
	var calls int
	h, _ := newHub(t, Options{Stored: func() { calls++ }})
	mustPublish(t, h, "t", "one")
	mustPublish(t, h, "t", "two")
	if _, err := h.Publish(context.Background(), draft("bad topic!", "x")); err == nil {
		t.Fatal("a bad topic was accepted")
	}
	if calls != 2 {
		t.Errorf("Stored called %d times, want 2", calls)
	}
}
