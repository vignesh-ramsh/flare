package auth

import (
	"sync"
	"time"
)

// Throttle counts failed login attempts and, past a limit, refuses further ones
// for a while, which makes guessing passwords or tokens over the network
// impractical. It is in memory: a restart clears it.
type Throttle struct {
	max     int
	window  time.Duration
	maxKeys int
	now     func() time.Time

	mu    sync.Mutex
	fails map[string][]time.Time // key -> times of recent failures, oldest first
}

// NewThrottle allows max failures per key within window.
func NewThrottle(max int, window time.Duration) *Throttle {
	return &Throttle{max: max, window: window, maxKeys: 10_000, now: time.Now, fails: map[string][]time.Time{}}
}

// Check reports whether key may try again, and if not, how long until it may.
func (t *Throttle) Check(key string) (wait time.Duration, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	recent := t.prune(key)
	if len(recent) < t.max {
		return 0, true
	}
	return recent[len(recent)-t.max].Add(t.window).Sub(t.now()), false
}

// Fail records a failed attempt by key.
func (t *Throttle) Fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, known := t.fails[key]; !known && len(t.fails) >= t.maxKeys {
		t.evict()
	}
	t.fails[key] = append(t.prune(key), t.now())
}

// prune drops attempts older than the window and returns the rest.
func (t *Throttle) prune(key string) []time.Time {
	cutoff := t.now().Add(-t.window)
	times := t.fails[key]
	i := 0
	for i < len(times) && !times[i].After(cutoff) {
		i++
	}
	times = times[i:]
	if len(times) == 0 {
		delete(t.fails, key)
		return nil
	}
	t.fails[key] = times
	return times
}

// evict makes room: it forgets keys whose failures have all expired and, if the
// table is still full, the keys with the oldest activity. Bounded memory matters
// more than perfect memory here, as the table is filled by whoever is attacking.
func (t *Throttle) evict() {
	for key := range t.fails {
		t.prune(key)
	}
	for key := range t.fails {
		if len(t.fails) < t.maxKeys*9/10 {
			return
		}
		delete(t.fails, key)
	}
}
