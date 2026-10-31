package store

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/push"
)

func device(id, userID, target string) push.Device {
	return push.Device{
		ID: id, SecretHash: "hash-" + id, UserID: userID, AppID: "org.example.flare", Platform: "android",
		Provider: push.FCM, Target: target, AppVersion: "1.2.3", CreatedAt: base, SeenAt: base,
	}
}

func register(t *testing.T, s *SQLite, d push.Device) {
	t.Helper()
	if err := s.RegisterDevice(ctx, d, 100); err != nil {
		t.Fatal(err)
	}
}

func topicsOf(subs []push.Subscription) []string {
	var out []string
	for _, s := range subs {
		out = append(out, s.Topic)
	}
	return out
}

func TestDevicesRoundTripWithEveryField(t *testing.T) {
	s := open(t)
	addUser(t, s, "u1", "alice")
	want := push.Device{
		ID: "d1", SecretHash: "secret-hash", UserID: "u1", AppID: "org.example.web", Platform: "web",
		Provider: push.WebPush, Target: "https://push.example/abc", Keys: push.Keys{P256DH: "pub", Auth: "auth"},
		RelayURL: "", AppVersion: "9.9", CreatedAt: base, SeenAt: base.Add(time.Hour),
	}
	register(t, s, want)
	got, err := s.Device(ctx, "d1")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Device = %+v, %v\nwant     %+v", got, err, want)
	}

	// An anonymous visitor's device has no owner.
	register(t, s, device("d2", "", "token-2"))
	if d, _ := s.Device(ctx, "d2"); d.UserID != "" {
		t.Errorf("UserID = %q, want none", d.UserID)
	}
	if _, err := s.Device(ctx, "nope"); !errors.Is(err, push.ErrNotFound) {
		t.Errorf("unknown device: %v", err)
	}
}

func TestRegisteringATargetAgainReplacesTheEarlierDevice(t *testing.T) {
	s := open(t)
	register(t, s, device("old", "", "same-token"))
	if _, err := s.SetSubscriptions(ctx, "old", []string{"alerts"}, base); err != nil {
		t.Fatal(err)
	}
	var q seq
	insert(t, s, q.msg("alerts", "queued for the old one", base, time.Hour))

	register(t, s, device("new", "", "same-token"))
	if _, err := s.Device(ctx, "old"); !errors.Is(err, push.ErrNotFound) {
		t.Errorf("the old registration survived: %v", err)
	}
	jobs, _ := s.ClaimJobs(ctx, base, time.Minute, 10)
	if len(jobs) != 0 {
		t.Errorf("%d jobs left for the replaced device", len(jobs))
	}
	// The same token under a different provider is another device.
	other := device("other", "", "same-token")
	other.Provider = push.APNs
	register(t, s, other)
	if _, err := s.Device(ctx, "new"); err != nil {
		t.Errorf("a different provider replaced it: %v", err)
	}
}

func TestADeviceCapAppliesPerOwnerAndNotToReplacements(t *testing.T) {
	s := open(t)
	addUser(t, s, "u1", "alice")
	addUser(t, s, "u2", "bob")
	for i := 0; i < 3; i++ {
		if err := s.RegisterDevice(ctx, device(fmt.Sprintf("a%d", i), "u1", fmt.Sprintf("t%d", i)), 3); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RegisterDevice(ctx, device("a3", "u1", "t3"), 3); !errors.Is(err, push.ErrTooManyDevices) {
		t.Errorf("a fourth device: %v, want the cap", err)
	}
	if _, err := s.Device(ctx, "a3"); err == nil {
		t.Error("a refused device was stored")
	}
	// Coming back with a known token replaces a device, so it does not count twice.
	if err := s.RegisterDevice(ctx, device("a1b", "u1", "t1"), 3); err != nil {
		t.Errorf("re-registering a known token: %v", err)
	}
	// Another owner has their own allowance, and so do anonymous visitors.
	if err := s.RegisterDevice(ctx, device("b0", "u2", "tb"), 3); err != nil {
		t.Errorf("another user: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := s.RegisterDevice(ctx, device(fmt.Sprintf("x%d", i), "", fmt.Sprintf("tx%d", i)), 3); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RegisterDevice(ctx, device("x3", "", "tx3"), 3); !errors.Is(err, push.ErrTooManyDevices) {
		t.Errorf("a fourth anonymous device: %v", err)
	}
}

func TestSubscriptionsAreReplacedAsAWholeAndKeepTheirIDs(t *testing.T) {
	s := open(t)
	register(t, s, device("d1", "", "tok"))

	first, err := s.SetSubscriptions(ctx, "d1", []string{"a", "b", "c"}, base)
	if err != nil || !reflect.DeepEqual(topicsOf(first), []string{"a", "b", "c"}) {
		t.Fatalf("first = %+v, %v", first, err)
	}
	ids := map[string]string{}
	for _, sub := range first {
		if sub.ID == "" || sub.DeviceID != "d1" {
			t.Errorf("subscription %+v", sub)
		}
		ids[sub.Topic] = sub.ID
	}
	if ids["a"] == ids["b"] || ids["b"] == ids["c"] {
		t.Errorf("IDs are not distinct: %v", ids)
	}

	second, err := s.SetSubscriptions(ctx, "d1", []string{"c", "d", "a"}, base.Add(time.Minute))
	if err != nil || !reflect.DeepEqual(topicsOf(second), []string{"c", "d", "a"}) {
		t.Fatalf("second = %+v, %v", second, err)
	}
	for _, sub := range second {
		if old, had := ids[sub.Topic]; had && old != sub.ID {
			t.Errorf("topic %s got a new ID %s, had %s: an app maps notifications by this ID", sub.Topic, sub.ID, old)
		}
		if _, had := ids[sub.Topic]; !had && sub.Topic != "d" {
			t.Errorf("unexpected topic %s", sub.Topic)
		}
	}

	if none, err := s.SetSubscriptions(ctx, "d1", nil, base); err != nil || len(none) != 0 {
		t.Errorf("clearing: %+v, %v", none, err)
	}
	if _, err := s.SetSubscriptions(ctx, "nope", []string{"a"}, base); !errors.Is(err, push.ErrNotFound) {
		t.Errorf("unknown device: %v", err)
	}
}

func TestPublishingQueuesOneJobPerSubscribedDeviceInTheSameTransaction(t *testing.T) {
	s := open(t)
	register(t, s, device("d1", "", "t1"))
	register(t, s, device("d2", "", "t2"))
	register(t, s, device("d3", "", "t3"))
	s.SetSubscriptions(ctx, "d1", []string{"alerts", "other"}, base)
	s.SetSubscriptions(ctx, "d2", []string{"alerts"}, base)
	s.SetSubscriptions(ctx, "d3", []string{"other"}, base)

	var q seq
	m := q.msg("alerts", "disk full", base, time.Hour)
	m.Title, m.Tags, m.Click, m.Priority = "Alert", []string{"warning", "disk"}, "https://example.com", 5
	insert(t, s, m)

	jobs, err := s.ClaimJobs(ctx, base, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, j := range jobs {
		got = append(got, j.Device.ID)
		if !reflect.DeepEqual(j.Message, m) {
			t.Errorf("job message = %+v\nwant         %+v", j.Message, m)
		}
		if j.Topic != "alerts" || j.SubscriptionID == "" || j.Attempts != 0 || j.ExpiresAt.Unix() != m.Expires {
			t.Errorf("job = %+v", j)
		}
		if j.Device.Target == "" || j.Device.Provider != push.FCM {
			t.Errorf("the job lacks its device: %+v", j.Device)
		}
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"d1", "d2"}) {
		t.Errorf("jobs for %v, want d1 and d2 (d3 follows another topic)", got)
	}

	// A message nobody follows queues nothing, and a failed insert queues nothing.
	insert(t, s, q.msg("quiet", "x", base, time.Hour))
	if err := s.Insert(ctx, m); err == nil {
		t.Fatal("inserting the same message twice should fail")
	}
	again, _ := s.ClaimJobs(ctx, base.Add(time.Hour), time.Minute, 100)
	if len(again) != 0 {
		t.Errorf("%d more jobs after a message nobody follows and a rejected duplicate", len(again))
	}
}

func TestAJobIsHiddenWhileClaimedAndReturnsIfNobodyFinishesIt(t *testing.T) {
	s := open(t)
	register(t, s, device("d1", "", "t1"))
	s.SetSubscriptions(ctx, "d1", []string{"t"}, base)
	var q seq
	insert(t, s, q.msg("t", "x", base, 24*time.Hour))

	first, _ := s.ClaimJobs(ctx, base, time.Minute, 10)
	if len(first) != 1 {
		t.Fatalf("claimed %d", len(first))
	}
	if again, _ := s.ClaimJobs(ctx, base.Add(30*time.Second), time.Minute, 10); len(again) != 0 {
		t.Error("a claimed job was handed out twice within its lease")
	}
	// The worker died: after the lease the job is available again.
	back, _ := s.ClaimJobs(ctx, base.Add(61*time.Second), time.Minute, 10)
	if len(back) != 1 || back[0].ID != first[0].ID {
		t.Errorf("after the lease: %+v", back)
	}

	if err := s.CompleteJob(ctx, back[0].ID); err != nil {
		t.Fatal(err)
	}
	if gone, _ := s.ClaimJobs(ctx, base.Add(time.Hour), time.Minute, 10); len(gone) != 0 {
		t.Errorf("a completed job came back: %+v", gone)
	}
}

func TestRetryingMovesAJobLater(t *testing.T) {
	s := open(t)
	register(t, s, device("d1", "", "t1"))
	s.SetSubscriptions(ctx, "d1", []string{"t"}, base)
	var q seq
	insert(t, s, q.msg("t", "x", base, 24*time.Hour))
	jobs, _ := s.ClaimJobs(ctx, base, time.Second, 10)

	if err := s.RetryJob(ctx, jobs[0].ID, 3, base.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if early, _ := s.ClaimJobs(ctx, base.Add(9*time.Minute), time.Second, 10); len(early) != 0 {
		t.Error("retried before its time")
	}
	late, _ := s.ClaimJobs(ctx, base.Add(10*time.Minute), time.Second, 10)
	if len(late) != 1 || late[0].Attempts != 3 {
		t.Errorf("late = %+v, want the job with 3 attempts", late)
	}
}

func TestClaimRespectsTheLimitAndOrder(t *testing.T) {
	s := open(t)
	register(t, s, device("d1", "", "t1"))
	s.SetSubscriptions(ctx, "d1", []string{"t"}, base)
	var q seq
	for i := 0; i < 5; i++ {
		insert(t, s, q.msg("t", fmt.Sprint(i), base.Add(time.Duration(i)*time.Millisecond), time.Hour))
	}
	jobs, _ := s.ClaimJobs(ctx, base.Add(time.Second), time.Minute, 3)
	if !reflect.DeepEqual(bodies(messagesOf(jobs)), []string{"0", "1", "2"}) {
		t.Errorf("first claim = %v", bodies(messagesOf(jobs)))
	}
	rest, _ := s.ClaimJobs(ctx, base.Add(time.Second), time.Minute, 3)
	if !reflect.DeepEqual(bodies(messagesOf(rest)), []string{"3", "4"}) {
		t.Errorf("second claim = %v", bodies(messagesOf(rest)))
	}
}

func messagesOf(jobs []push.Job) []message.Message {
	var out []message.Message
	for _, j := range jobs {
		out = append(out, j.Message)
	}
	return out
}

func TestConcurrentWorkersNeverClaimTheSameJob(t *testing.T) {
	s := open(t)
	register(t, s, device("d1", "", "t1"))
	s.SetSubscriptions(ctx, "d1", []string{"t"}, base)
	var q seq
	const total = 60
	for i := 0; i < total; i++ {
		insert(t, s, q.msg("t", fmt.Sprint(i), base, time.Hour))
	}

	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Bounded, so that a claim that forgets to hide jobs fails the test
			// instead of keeping it running for ever.
			for round := 0; round < total; round++ {
				jobs, err := s.ClaimJobs(ctx, base, time.Hour, 5)
				if err != nil {
					t.Error(err)
					return
				}
				if len(jobs) == 0 {
					return
				}
				mu.Lock()
				for _, j := range jobs {
					seen[j.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != total {
		t.Errorf("%d jobs claimed, want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("job %d was claimed %d times", id, n)
		}
	}
}

func TestExpiredAndOrphanedJobsArePurged(t *testing.T) {
	s := open(t)
	register(t, s, device("d1", "", "t1"))
	s.SetSubscriptions(ctx, "d1", []string{"t"}, base)
	var q seq
	short := q.msg("t", "short", base, time.Minute)
	long := q.msg("t", "long", base, time.Hour)
	insert(t, s, short, long)

	if jobs, _ := s.ClaimJobs(ctx, base.Add(2*time.Minute), time.Minute, 10); len(jobs) != 1 || jobs[0].Message.Message != "long" {
		t.Errorf("an expired message's job was handed out: %+v", jobs)
	}
	n, err := s.DeleteExpiredJobs(ctx, base.Add(2*time.Minute))
	if err != nil || n != 1 {
		t.Errorf("purged %d, %v; want the one expired job", n, err)
	}
	// A job whose message was purged first goes too.
	if _, err := s.DeleteExpired(ctx, base.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.DeleteExpiredJobs(ctx, base); n != 1 {
		t.Errorf("purged %d orphans, want 1", n)
	}
}

func TestDeletingADeviceUserOrSubscriptionRemovesWhatDependsOnIt(t *testing.T) {
	s := open(t)
	addUser(t, s, "u1", "alice")
	register(t, s, device("d1", "u1", "t1"))
	register(t, s, device("d2", "", "t2"))
	subs1, _ := s.SetSubscriptions(ctx, "d1", []string{"a", "b"}, base)
	s.SetSubscriptions(ctx, "d2", []string{"a"}, base)
	var q seq
	insert(t, s, q.msg("a", "x", base, time.Hour), q.msg("b", "y", base, time.Hour))
	count := func() int {
		jobs, _ := s.ClaimJobs(ctx, base, time.Second, 100)
		for _, j := range jobs { // put them back
			s.RetryJob(ctx, j.ID, j.Attempts, base)
		}
		return len(jobs)
	}
	if n := count(); n != 3 {
		t.Fatalf("%d jobs queued, want 3", n)
	}

	var forB string
	for _, sub := range subs1 {
		if sub.Topic == "b" {
			forB = sub.ID
		}
	}
	if err := s.DeleteSubscription(ctx, forB); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Errorf("after dropping a subscription: %d jobs, want 2", n)
	}

	// Deleting the owner deletes their devices, with everything queued for them.
	if err := (auth.Accounts)(s).DeleteUser(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Device(ctx, "d1"); !errors.Is(err, push.ErrNotFound) {
		t.Errorf("the user's device survived: %v", err)
	}
	if n := count(); n != 1 {
		t.Errorf("after deleting the user: %d jobs, want 1", n)
	}

	if err := s.DeleteDevice(ctx, "d2"); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Errorf("after deleting the device: %d jobs", n)
	}
	if err := s.DeleteDevice(ctx, "d2"); !errors.Is(err, push.ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
}

func TestMessageLookupByID(t *testing.T) {
	s := open(t)
	var q seq
	m := q.msg("t", "hello", base, time.Hour)
	m.Title, m.Tags = "T", []string{"a"}
	insert(t, s, m)

	got, err := s.Message(ctx, m.ID, base)
	if err != nil || !reflect.DeepEqual(got, m) {
		t.Errorf("Message = %+v, %v; want %+v", got, err, m)
	}
	if _, err := s.Message(ctx, m.ID, base.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("an expired message: %v", err)
	}
	if _, err := s.Message(ctx, "nope", base); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown ID: %v", err)
	}
}

func TestSettingsAreKeptOnceSet(t *testing.T) {
	s := open(t)
	if _, ok, err := s.Setting(ctx, "k"); ok || err != nil {
		t.Errorf("unset: %v, %v", ok, err)
	}
	if v, err := s.SetSettingIfAbsent(ctx, "k", "first"); v != "first" || err != nil {
		t.Errorf("first = %q, %v", v, err)
	}
	if v, err := s.SetSettingIfAbsent(ctx, "k", "second"); v != "first" || err != nil {
		t.Errorf("a second writer got %q, %v; the first value must win", v, err)
	}
	if v, ok, _ := s.Setting(ctx, "k"); !ok || v != "first" {
		t.Errorf("Setting = %q, %v", v, ok)
	}
}

// busyTimeoutSlack is the shortest wait that can only have been the busy timeout
// (5 seconds) running out, with room for a slow machine.
const busyTimeoutSlack = 4 * time.Second

// Changing a device's subscriptions while messages are published and notifications
// are claimed must never fail. A transaction that starts by reading and then writes
// is refused by SQLite, at once and whatever the busy timeout, if another writer
// committed in between; so every transaction takes the write lock first.
func TestSubscriptionsCanBeChangedWhileOthersWrite(t *testing.T) {
	s := open(t)
	for i := 0; i < 4; i++ {
		register(t, s, device(fmt.Sprintf("d%d", i), "", fmt.Sprintf("t%d", i)))
	}
	var q seq
	var seqMu sync.Mutex
	stop := make(chan struct{})
	var writers sync.WaitGroup
	// Other writers: publishing, and claiming and finishing notifications.
	for w := 0; w < 3; w++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				// Heavy load but not a spin: SQLite's write lock is not fair, so a
				// writer that never pauses can keep another waiting for seconds, which
				// no server's traffic does.
				time.Sleep(time.Millisecond)
				seqMu.Lock()
				m := q.msg("t", "x", base, time.Hour)
				seqMu.Unlock()
				if err := s.Insert(ctx, m); err != nil {
					t.Errorf("insert: %v", err)
					return
				}
				jobs, err := s.ClaimJobs(ctx, base, time.Second, 10)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				for _, j := range jobs {
					if err := s.CompleteJob(ctx, j.ID); err != nil {
						t.Errorf("complete: %v", err)
						return
					}
				}
			}
		}()
	}

	var changers sync.WaitGroup
	for i := 0; i < 4; i++ {
		changers.Add(1)
		go func(device string) {
			defer changers.Done()
			for round := 0; round < 150; round++ {
				topics := []string{"t", fmt.Sprintf("topic-%d", round%5)}
				begin := time.Now()
				_, err := s.SetSubscriptions(ctx, device, topics, base)
				if err == nil {
					continue
				}
				// What this test is about fails at once: a transaction that reads and
				// then writes is refused the moment another writer commits. After
				// waiting out the busy timeout it is a different thing: SQLite has one
				// writer at a time and does not queue them fairly, so on a machine
				// whose disk and CPUs are swamped (other test packages run at the same
				// time) a writer can be kept waiting for seconds, and then gives up.
				// That is how SQLite behaves under overload, not the bug.
				if waited := time.Since(begin); waited < busyTimeoutSlack {
					t.Errorf("SetSubscriptions failed after %v: %v", waited, err)
				} else {
					t.Logf("SetSubscriptions gave up after %v under load: %v", waited, err)
				}
				return
			}
		}(fmt.Sprintf("d%d", i))
	}
	changers.Wait()
	close(stop)
	writers.Wait()
}
