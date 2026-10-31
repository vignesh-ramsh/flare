package relay

import (
	"fmt"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestALimiterAllowsABurstThenTheRate(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	l := NewLimiter(60, clk.Now) // one a second, after a burst of 15

	for i := 0; i < 15; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d of the burst was refused", i+1)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait != time.Second {
		t.Fatalf("the 16th: ok=%v wait=%v; want refused, with a second to wait", ok, wait)
	}

	clk.advance(1500 * time.Millisecond)
	if ok, _ := l.Allow("a"); !ok {
		t.Error("a token should have come back after 1.5 seconds")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Error("only one token came back")
	}
	clk.advance(time.Hour)
	for i := 0; i < 15; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("after an hour the burst should be back; request %d refused", i+1)
		}
	}
	if ok, _ := l.Allow("a"); ok {
		t.Error("the bucket holds no more than the burst, however long it was idle")
	}
}

func TestKeysDoNotShareABucket(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	l := NewLimiter(4, clk.Now) // a burst of 5? no: min(4, max(5, 1)) = 4
	for i := 0; i < 4; i++ {
		l.Allow("a")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("a should be out of tokens")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Error("b was refused because of a")
	}
}

func TestTheWaitIsHowLongUntilATokenIsBack(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	l := NewLimiter(6, clk.Now) // one every ten seconds; the burst is 5? min(6, max(5, 1.5)) = 5
	for i := 0; i < 5; i++ {
		l.Allow("k")
	}
	if ok, wait := l.Allow("k"); ok || wait != 10*time.Second {
		t.Errorf("ok=%v wait=%v, want 10s", ok, wait)
	}
	clk.advance(4 * time.Second)
	if ok, wait := l.Allow("k"); ok || wait != 6*time.Second {
		t.Errorf("ok=%v wait=%v, want 6s", ok, wait)
	}
}

func TestAnIdleKeyIsForgottenButABusyOneIsNot(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	l := NewLimiter(60, clk.Now)
	for i := 0; i < 15; i++ {
		l.Allow("busy") // empty
	}
	for i := 0; i < sweepAbove; i++ {
		l.Allow(fmt.Sprintf("idle-%d", i)) // each uses one token
	}
	clk.advance(time.Minute) // every idle key has refilled
	l.Allow("fresh")         // the table is full, so this sweeps
	if n := l.size(); n > 5 {
		t.Errorf("%d keys tracked after a sweep, want the refilled ones forgotten", n)
	}
	if ok, _ := l.Allow("busy"); !ok {
		t.Error("busy had a minute to refill") // it did: so it is allowed, and was swept as full
	}
}
