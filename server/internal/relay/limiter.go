package relay

import (
	"math"
	"sync"
	"time"
)

// Limiter is a token bucket per key: each key may make a burst of requests, and
// after that as many per minute as the rate allows.
type Limiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	now     func() time.Time
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

// sweepAbove is how many keys the limiter keeps before it forgets the idle ones.
const sweepAbove = 4096

// NewLimiter returns a Limiter that allows perMinute requests a minute for each
// key, after a burst of a quarter of that (at least 5, and never more than a
// minute's worth). now is time.Now outside tests.
func NewLimiter(perMinute int, now func() time.Time) *Limiter {
	burst := math.Min(float64(perMinute), math.Max(5, math.Floor(float64(perMinute)/4)))
	return &Limiter{rate: float64(perMinute) / 60, burst: burst, now: now, buckets: map[string]*bucket{}}
}

// Allow says whether key may make a request now. If not, it also says how long
// until it may.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= sweepAbove {
			l.sweep(now)
		}
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.at).Seconds()*l.rate)
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration(math.Ceil((1 - b.tokens) / l.rate * float64(time.Second)))
	return false, max(wait, time.Second)
}

// sweep forgets the keys whose buckets have refilled: a key that is not there
// starts with a full bucket anyway.
func (l *Limiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.at).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

// size is how many keys are being tracked, for tests.
func (l *Limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
