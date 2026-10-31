package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newThrottle(max int, window time.Duration) (*Throttle, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	t := NewThrottle(max, window)
	t.now = clk.now
	return t, clk
}

func TestThrottleBlocksAfterTooManyFailures(t *testing.T) {
	th, _ := newThrottle(3, 10*time.Minute)
	for i := range 3 {
		if _, ok := th.Check("a"); !ok {
			t.Fatalf("blocked after only %d failures", i)
		}
		th.Fail("a")
	}
	wait, ok := th.Check("a")
	if ok || wait != 10*time.Minute {
		t.Errorf("Check = %v, %v; want blocked for the whole window", wait, ok)
	}
}

func TestThrottleCountsEachKeySeparately(t *testing.T) {
	th, _ := newThrottle(2, time.Minute)
	th.Fail("a")
	th.Fail("a")
	if _, ok := th.Check("a"); ok {
		t.Error("a should be blocked")
	}
	if _, ok := th.Check("b"); !ok {
		t.Error("b was blocked by a's failures")
	}
}

func TestThrottleForgetsOldFailuresAndReportsTheWait(t *testing.T) {
	th, clk := newThrottle(2, 10*time.Minute)
	th.Fail("a")
	clk.advance(4 * time.Minute)
	th.Fail("a")
	if wait, ok := th.Check("a"); ok || wait != 6*time.Minute {
		t.Errorf("Check = %v, %v; want blocked for 6m more: until the oldest failure leaves the window", wait, ok)
	}
	clk.advance(6*time.Minute + time.Second)
	if _, ok := th.Check("a"); !ok {
		t.Error("still blocked after the oldest failure left the window")
	}
	th.Fail("a") // one old failure remains (4m ago + 6m = 10m ago... gone); this is the second in the window
	if _, ok := th.Check("a"); ok {
		t.Error("expected a block after the second failure within the window")
	}
	clk.advance(11 * time.Minute)
	if _, ok := th.Check("a"); !ok {
		t.Error("not forgiven after a quiet window")
	}
}

func TestThrottleKeepsMemoryBounded(t *testing.T) {
	th, clk := newThrottle(5, time.Minute)
	th.maxKeys = 100
	for i := range 5000 {
		th.Fail(fmt.Sprintf("10.%d.%d.1", i/256, i%256))
	}
	th.mu.Lock()
	n := len(th.fails)
	th.mu.Unlock()
	if n > 100 {
		t.Errorf("tracking %d keys, want at most 100", n)
	}
	clk.advance(2 * time.Minute)
	th.Fail("fresh")
	th.mu.Lock()
	defer th.mu.Unlock()
	for key := range th.fails {
		th.prune(key)
	}
	if len(th.fails) != 1 {
		t.Errorf("%d keys remain after everything expired, want only the fresh one", len(th.fails))
	}
}

func TestThrottleIsSafeForConcurrentUse(t *testing.T) {
	th := NewThrottle(5, time.Minute)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				key := fmt.Sprint(g, i%7)
				th.Check(key)
				th.Fail(key)
			}
		}()
	}
	wg.Wait()
}
