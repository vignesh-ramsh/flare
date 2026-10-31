package push_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/id"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/push"
	"github.com/vignesh-ramsh/flare/server/internal/store"
)

// scripted is a Sender whose behaviour a test controls.
type scripted struct {
	mu   sync.Mutex
	max  int
	sent []push.Notification
	fn   func(push.Notification) error
}

func (s *scripted) MaxPayload() int {
	if s.max > 0 {
		return s.max
	}
	return 4000
}

func (s *scripted) Send(ctx context.Context, n push.Notification) error {
	s.mu.Lock()
	s.sent = append(s.sent, n)
	fn := s.fn
	s.mu.Unlock()
	if fn != nil {
		return fn(n)
	}
	return nil
}

func (s *scripted) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func (s *scripted) all() []push.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]push.Notification(nil), s.sent...)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type rig struct {
	t      *testing.T
	store  *store.SQLite
	sender *scripted
	access *fakeAccess
	clock  *clock
	disp   *push.Dispatcher
	gen    id.Generator
	logs   *logBuffer
}

type logBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newRig(t *testing.T, edit ...func(*push.DispatcherConfig)) *rig {
	t.Helper()
	r := &rig{
		t: t, store: newStore(t), sender: &scripted{}, access: &fakeAccess{},
		clock: &clock{t: time.Unix(1_800_000_000, 0)}, logs: &logBuffer{},
	}
	reg := push.NewRegistry()
	reg.Add(push.UnifiedPush, r.sender)
	cfg := push.DispatcherConfig{
		Now: r.clock.Now, Poll: 10 * time.Millisecond, Workers: 4,
		Backoff: func(attempt int) time.Duration { return time.Duration(attempt) * time.Minute }, // predictable
		Log:     testLogger(r.logs),
	}
	for _, f := range edit {
		f(&cfg)
	}
	r.disp = push.NewDispatcher(r.store, reg, r.access, cfg)
	return r
}

// device registers a UnifiedPush device that follows topics, and lets its owner
// read them.
func (r *rig) device(deviceID, userID string, topics ...string) []push.Subscription {
	r.t.Helper()
	if userID != "" {
		u := auth.User{ID: userID, Name: "user-" + userID, Role: auth.RoleUser, CreatedAt: r.clock.Now()}
		if err := r.store.CreateUser(ctx, u, "hash"); err != nil && !errors.Is(err, auth.ErrExists) {
			r.t.Fatal(err)
		}
	}
	d := push.Device{
		ID: deviceID, SecretHash: "h", UserID: userID, AppID: "app", Platform: "android",
		Provider: push.UnifiedPush, Target: "https://up.example.com/" + deviceID, CreatedAt: r.clock.Now(), SeenAt: r.clock.Now(),
	}
	if err := r.store.RegisterDevice(ctx, d, 100); err != nil {
		r.t.Fatal(err)
	}
	subs, err := r.store.SetSubscriptions(ctx, deviceID, topics, r.clock.Now())
	if err != nil {
		r.t.Fatal(err)
	}
	for _, topic := range topics {
		r.access.set(userID, topic, true)
	}
	return subs
}

// publish stores a message the way the hub does, which queues its notifications,
// and wakes the dispatcher.
func (r *rig) publish(topic, body string, ttl time.Duration) message.Message {
	r.t.Helper()
	now := r.clock.Now()
	m := message.Message{
		ID: r.gen.New(now), Time: now.Unix(), Expires: now.Add(ttl).Unix(), Event: message.EventMessage,
		Topic: topic, Message: body, Priority: 3,
	}
	if err := r.store.Insert(ctx, m); err != nil {
		r.t.Fatal(err)
	}
	r.disp.Wake()
	return m
}

// run starts the dispatcher; the returned function stops it and waits.
func (r *rig) run() (stop func()) {
	c, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.disp.Run(c); close(done) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				r.t.Error("the dispatcher did not stop")
			}
		})
	}
	r.t.Cleanup(stop)
	return stop
}

func (r *rig) waitFor(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s\nlogs:\n%s", what, r.logs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// queued is how many notifications are still waiting, whenever they are due.
func (r *rig) queued() int {
	n, err := r.store.PendingJobs(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	return n
}

func TestANotificationIsSentOnceItsMessageIsStored(t *testing.T) {
	r := newRig(t, func(c *push.DispatcherConfig) { c.Poll = time.Hour }) // only Wake can trigger it
	subs := r.device("d1", "", "alerts")
	stop := r.run()
	defer stop()

	m := r.publish("alerts", "disk full", time.Hour)
	r.waitFor("the notification", func() bool { return r.sender.count() == 1 })
	n := r.sender.all()[0]

	if n.Device.ID != "d1" || n.SubscriptionID != subs[0].ID || n.Message.ID != m.ID || n.WakeUp {
		t.Errorf("notification = %+v", n)
	}
	var payload map[string]any
	if err := json.Unmarshal(n.Payload, &payload); err != nil {
		t.Fatalf("payload %q: %v", n.Payload, err)
	}
	if payload["event"] != "message" || payload["sub"] != subs[0].ID || payload["id"] != m.ID ||
		payload["topic"] != "alerts" || payload["message"] != "disk full" {
		t.Errorf("payload = %v", payload)
	}
	if n.TTL() != time.Hour {
		t.Errorf("TTL = %v, want the message's remaining life", n.TTL())
	}
	r.waitFor("the job to be removed", func() bool { return r.queued() == 0 })
}

func TestEverySubscribedDeviceIsNotifiedAndNoOneElse(t *testing.T) {
	r := newRig(t)
	r.device("d1", "", "alerts")
	r.device("d2", "", "alerts", "status")
	r.device("d3", "", "status")
	stop := r.run()
	defer stop()

	r.publish("alerts", "x", time.Hour)
	r.waitFor("two notifications", func() bool { return r.sender.count() == 2 })
	time.Sleep(50 * time.Millisecond) // a third, wrongly sent, would show by now
	got := map[string]int{}
	for _, n := range r.sender.all() {
		got[n.Device.ID]++
	}
	if len(got) != 2 || got["d1"] != 1 || got["d2"] != 1 {
		t.Errorf("sent to %v, want d1 and d2 once each", got)
	}
}

func TestAMessageTooBigForThePushServiceIsAnnouncedWithoutItsContent(t *testing.T) {
	r := newRig(t)
	r.sender.max = 300
	subs := r.device("d1", "", "alerts")
	stop := r.run()
	defer stop()

	small := r.publish("alerts", "short", time.Hour)
	big := r.publish("alerts", strings.Repeat("long ", 100), time.Hour)
	r.waitFor("both", func() bool { return r.sender.count() == 2 })

	byID := map[string]push.Notification{}
	for _, n := range r.sender.all() {
		byID[n.Message.ID] = n
	}
	if n := byID[small.ID]; n.WakeUp || !strings.Contains(string(n.Payload), "short") {
		t.Errorf("the small one: %+v", n)
	}
	n := byID[big.ID]
	if !n.WakeUp {
		t.Fatalf("the big one was sent whole: %s", n.Payload)
	}
	var payload map[string]string
	json.Unmarshal(n.Payload, &payload)
	want := map[string]string{"event": "poll_request", "sub": subs[0].ID, "id": big.ID}
	if len(payload) != 3 || payload["event"] != want["event"] || payload["sub"] != want["sub"] || payload["id"] != want["id"] {
		t.Errorf("poll request = %v, want %v (and nothing else: no topic, no content)", payload, want)
	}
	if strings.Contains(string(n.Payload), "alerts") || strings.Contains(string(n.Payload), "long") {
		t.Error("the poll request reveals the topic or the content")
	}
}

func TestContentFreeModeNeverSendsContent(t *testing.T) {
	r := newRig(t, func(c *push.DispatcherConfig) { c.ContentFree = true })
	r.device("d1", "", "alerts")
	stop := r.run()
	defer stop()
	r.publish("alerts", "a secret", time.Hour)
	r.waitFor("it", func() bool { return r.sender.count() == 1 })
	n := r.sender.all()[0]
	if !n.WakeUp || strings.Contains(string(n.Payload), "secret") || strings.Contains(string(n.Payload), "alerts") {
		t.Errorf("notification = %+v: %s", n, n.Payload)
	}
}

func TestAFailedSendIsRetriedLaterWithBackoffAndThenSucceeds(t *testing.T) {
	r := newRig(t)
	r.device("d1", "", "alerts")
	var failures atomic.Int32
	r.sender.fn = func(push.Notification) error {
		if failures.Add(1) <= 2 {
			return errors.New("503 from the push service")
		}
		return nil
	}
	stop := r.run()
	defer stop()

	r.publish("alerts", "x", 24*time.Hour)
	r.waitFor("the first attempt", func() bool { return r.sender.count() == 1 })
	time.Sleep(60 * time.Millisecond)
	if r.sender.count() != 1 {
		t.Fatalf("retried at once: %d attempts", r.sender.count())
	}

	r.clock.advance(61 * time.Second) // backoff after the first failure is a minute
	r.waitFor("the second attempt", func() bool { return r.sender.count() == 2 })
	time.Sleep(60 * time.Millisecond)
	r.clock.advance(61 * time.Second) // the second failure waits two minutes: too soon
	time.Sleep(60 * time.Millisecond)
	if r.sender.count() != 2 {
		t.Fatalf("the second retry came after %ds instead of 2 minutes", 61)
	}
	r.clock.advance(61 * time.Second)
	r.waitFor("the third attempt", func() bool { return r.sender.count() == 3 })
	r.waitFor("the job to be removed once it was sent", func() bool { return r.queued() == 0 })
}

func TestAServiceSAskToWaitIsHonoured(t *testing.T) {
	r := newRig(t)
	r.device("d1", "", "alerts")
	r.sender.fn = func(push.Notification) error {
		return push.Transient(errors.New("429"), 20*time.Minute)
	}
	stop := r.run()
	defer stop()
	r.publish("alerts", "x", 24*time.Hour)
	r.waitFor("the attempt", func() bool { return r.sender.count() == 1 })
	time.Sleep(50 * time.Millisecond)

	r.clock.advance(19 * time.Minute) // the backoff alone would have allowed this long ago
	time.Sleep(60 * time.Millisecond)
	if r.sender.count() != 1 {
		t.Error("retried before the time the service asked for")
	}
	r.clock.advance(2 * time.Minute)
	r.waitFor("the retry", func() bool { return r.sender.count() == 2 })
}

func TestARetryAfterOfAYearIsCapped(t *testing.T) {
	r := newRig(t)
	r.device("d1", "", "alerts")
	r.sender.fn = func(push.Notification) error {
		return push.Transient(errors.New("429"), 8760*time.Hour)
	}
	stop := r.run()
	defer stop()
	r.publish("alerts", "x", 24*time.Hour)
	r.waitFor("the attempt", func() bool { return r.sender.count() == 1 })
	time.Sleep(50 * time.Millisecond)
	// Capped at an hour, which is still inside the message's day, so it is retried
	// rather than given up on.
	if r.queued() != 1 {
		t.Errorf("%d jobs queued, want the notification to wait for a later retry", r.queued())
	}
}

func TestADeadlineEndsTheRetries(t *testing.T) {
	r := newRig(t)
	r.device("d1", "", "alerts")
	r.sender.fn = func(push.Notification) error { return errors.New("down") }
	stop := r.run()
	defer stop()

	r.publish("alerts", "x", 90*time.Second) // the message lives for a minute and a half
	r.waitFor("the first attempt", func() bool { return r.sender.count() == 1 })
	time.Sleep(50 * time.Millisecond)
	if r.queued() != 1 {
		t.Fatal("the first failure did not leave it queued")
	}
	r.clock.advance(61 * time.Second)
	r.waitFor("the second attempt", func() bool { return r.sender.count() == 2 })
	// Its next retry would be two minutes away, after the message expires: give up.
	r.waitFor("giving up", func() bool { return r.queued() == 0 })
	if !strings.Contains(r.logs.String(), "giving up") {
		t.Errorf("no word about giving up:\n%s", r.logs.String())
	}
}

func TestAPermanentFailureDropsTheNotificationButKeepsTheDevice(t *testing.T) {
	r := newRig(t)
	r.device("d1", "", "alerts")
	r.sender.fn = func(push.Notification) error { return push.Permanent(errors.New("payload too large")) }
	stop := r.run()
	defer stop()
	r.publish("alerts", "x", time.Hour)
	r.waitFor("the job to be dropped", func() bool { return r.sender.count() == 1 && r.queued() == 0 })
	if _, err := r.store.Device(ctx, "d1"); err != nil {
		t.Errorf("the device was forgotten: %v", err)
	}
}

func TestADeviceThePushServiceNoLongerKnowsIsForgottenWithEverythingQueuedForIt(t *testing.T) {
	r := newRig(t)
	r.device("gone", "", "alerts", "status")
	r.device("fine", "", "alerts")
	r.sender.fn = func(n push.Notification) error {
		if n.Device.ID == "gone" {
			return push.Gone(errors.New("410 Unregistered"))
		}
		return nil
	}
	r.publish("alerts", "1", time.Hour)
	r.publish("status", "2", time.Hour) // also queued for the device that is about to be found dead
	stop := r.run()
	defer stop()

	r.waitFor("the device to be forgotten", func() bool {
		_, err := r.store.Device(ctx, "gone")
		return errors.Is(err, push.ErrNotFound)
	})
	r.waitFor("the other device to be served", func() bool { return r.queued() == 0 })
	if _, err := r.store.Device(ctx, "fine"); err != nil {
		t.Errorf("another device was forgotten too: %v", err)
	}
	for _, n := range r.sender.all() {
		if n.Device.ID == "gone" && n.Message.Message == "2" && r.sender.count() > 3 {
			t.Error("the dead device was tried again for its other notification")
		}
	}
}

func TestAccessIsCheckedAgainWhenTheNotificationIsSent(t *testing.T) {
	r := newRig(t)
	r.device("d1", "u1", "payroll")
	r.device("d2", "u2", "payroll")
	r.publish("payroll", "salaries", time.Hour)
	r.access.set("u1", "payroll", false) // u1 lost access after subscribing, before the send
	stop := r.run()
	defer stop()

	r.waitFor("the permitted device to be notified", func() bool { return r.sender.count() == 1 })
	time.Sleep(60 * time.Millisecond)
	if n := r.sender.all(); len(n) != 1 || n[0].Device.ID != "d2" {
		t.Fatalf("sent %+v, want only d2", n)
	}
	// And the subscription is dropped, so nothing more is queued for u1's device.
	r.publish("payroll", "more salaries", time.Hour)
	r.waitFor("d2 again", func() bool { return r.sender.count() == 2 })
	time.Sleep(60 * time.Millisecond)
	for _, n := range r.sender.all() {
		if n.Device.ID == "d1" {
			t.Errorf("the device that lost access was sent %q", n.Message.Message)
		}
	}
	if subs, _ := r.store.SetSubscriptions(ctx, "d1", nil, r.clock.Now()); len(subs) != 0 {
		t.Error("unexpected subscriptions")
	}
}

func TestAnAnonymousDevicesAccessIsCheckedToo(t *testing.T) {
	r := newRig(t)
	r.device("anon", "", "news")
	r.publish("news", "x", time.Hour)
	r.access.set("", "news", false) // the server stopped letting anonymous visitors read it
	stop := r.run()
	defer stop()
	r.waitFor("the job to go", func() bool { return r.queued() == 0 })
	if r.sender.count() != 0 {
		t.Errorf("sent %d notifications to a device that may no longer read", r.sender.count())
	}
}

func TestNotBeingAbleToCheckAccessDelaysTheNotificationInsteadOfDroppingIt(t *testing.T) {
	r := newRig(t)
	r.device("d1", "", "alerts")
	r.access.err = errors.New("database is locked")
	r.publish("alerts", "x", time.Hour)
	stop := r.run()
	defer stop()
	r.waitFor("the failure to be noticed", func() bool { return strings.Contains(r.logs.String(), "could not check") })
	time.Sleep(50 * time.Millisecond)
	if r.sender.count() != 0 || r.queued() != 1 {
		t.Fatalf("sent %d, queued %d: a lookup failure must neither send nor drop", r.sender.count(), r.queued())
	}

	r.access.mu.Lock()
	r.access.err = nil
	r.access.mu.Unlock()
	r.clock.advance(time.Minute)
	r.waitFor("it to go once access can be checked", func() bool { return r.sender.count() == 1 })
}

func TestADeviceWhoseProviderIsNoLongerConfiguredLosesTheNotificationNotTheDevice(t *testing.T) {
	r := newRig(t)
	r.device("d1", "", "alerts")
	// The same device but with a provider the registry has no sender for.
	d := push.Device{ID: "orphan", SecretHash: "h", AppID: "app", Platform: "android", Provider: push.FCM, Target: "tok-0123456789abcdef", CreatedAt: r.clock.Now(), SeenAt: r.clock.Now()}
	if err := r.store.RegisterDevice(ctx, d, 100); err != nil {
		t.Fatal(err)
	}
	r.store.SetSubscriptions(ctx, "orphan", []string{"alerts"}, r.clock.Now())
	r.access.set("", "alerts", true)
	r.publish("alerts", "x", time.Hour)
	stop := r.run()
	defer stop()
	r.waitFor("the queue to empty", func() bool { return r.queued() == 0 })
	if r.sender.count() != 1 {
		t.Errorf("sent %d, want one (the device that can be reached)", r.sender.count())
	}
	if _, err := r.store.Device(ctx, "orphan"); err != nil {
		t.Errorf("the device was forgotten: %v", err)
	}
}

func TestManyNotificationsAreEachSentExactlyOnce(t *testing.T) {
	r := newRig(t, func(c *push.DispatcherConfig) { c.Workers = 6; c.Batch = 7 })
	const devices, messages = 12, 20
	for i := 0; i < devices; i++ {
		r.device(fmt.Sprintf("d%02d", i), "", "alerts")
	}
	for i := 0; i < messages; i++ {
		r.publish("alerts", fmt.Sprint(i), time.Hour)
	}
	stop := r.run()
	defer stop()
	r.waitFor("all of them", func() bool { return r.queued() == 0 && r.sender.count() >= devices*messages })
	time.Sleep(50 * time.Millisecond)

	seen := map[string]int{}
	for _, n := range r.sender.all() {
		seen[n.Device.ID+"/"+n.Message.ID]++
	}
	if len(seen) != devices*messages {
		t.Errorf("%d distinct notifications, want %d", len(seen), devices*messages)
	}
	for k, c := range seen {
		if c != 1 {
			t.Errorf("%s was sent %d times", k, c)
		}
	}
}

func TestShuttingDownInTheMiddleOfASendDoesNotLoseTheNotification(t *testing.T) {
	r := newRig(t)
	r.device("d1", "", "alerts")
	started := make(chan struct{})
	r.sender.fn = func(n push.Notification) error {
		select {
		case started <- struct{}{}:
		default:
		}
		time.Sleep(200 * time.Millisecond)
		return context.Canceled // as a real sender's request would fail when its context ends
	}
	r.publish("alerts", "x", time.Hour)
	stop := r.run()
	<-started
	stop() // the server is shutting down while the send is under way

	if r.queued() != 1 {
		t.Fatalf("%d jobs queued after shutdown, want the interrupted one kept", r.queued())
	}
	again, err := r.store.ClaimJobs(ctx, r.clock.Now().Add(3*time.Minute), 0, 10)
	if err != nil || len(again) != 1 || again[0].Attempts != 0 {
		t.Fatalf("after shutdown: %+v, %v; want the job back with no failed attempt counted", again, err)
	}
	// After its lease runs out (the next run), it is sent again, and with no
	// attempt counted against it.
	r.sender.fn = nil
	r.clock.advance(3 * time.Minute)
	stop2 := r.run()
	defer stop2()
	r.waitFor("the resent notification", func() bool { return r.sender.count() >= 2 })
	if got := r.sender.all()[1]; got.Message.Message != "x" {
		t.Errorf("resent %+v", got)
	}
}

func TestRunStopsPromptlyWhenIdle(t *testing.T) {
	r := newRig(t, func(c *push.DispatcherConfig) { c.Poll = time.Hour })
	stop := r.run()
	time.Sleep(30 * time.Millisecond)
	start := time.Now()
	stop()
	if time.Since(start) > time.Second {
		t.Errorf("stopping took %v", time.Since(start))
	}
}

func TestDefaultBackoffGrowsIsCappedAndVaries(t *testing.T) {
	prev := time.Duration(0)
	for attempt := 1; attempt <= 5; attempt++ {
		lo, hi := time.Duration(1<<62), time.Duration(0)
		for i := 0; i < 200; i++ {
			d := push.DefaultBackoff(attempt)
			lo, hi = min(lo, d), max(hi, d)
		}
		base := 10 * time.Second << (attempt - 1)
		if lo < base*3/4 || hi > base*5/4 {
			t.Errorf("attempt %d: %v..%v outside a quarter either side of %v", attempt, lo, hi, base)
		}
		if lo == hi {
			t.Errorf("attempt %d: no jitter", attempt)
		}
		if lo <= prev/2 {
			t.Errorf("attempt %d did not wait longer than the one before", attempt)
		}
		prev = lo
	}
	for _, attempt := range []int{12, 50, 1000} {
		if d := push.DefaultBackoff(attempt); d > 30*time.Minute*5/4 {
			t.Errorf("attempt %d waits %v, beyond the cap", attempt, d)
		}
	}
}
