package push

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

// DispatcherConfig tunes a Dispatcher. Zero values get defaults.
type DispatcherConfig struct {
	// Workers is how many notifications are sent at once.
	Workers int
	// Batch is how many due notifications are taken from the queue at a time.
	Batch int
	// Lease is how long a taken notification is kept from other workers. If the
	// server dies while sending it, it is sent again after this long.
	Lease time.Duration
	// Poll is how often the queue is looked at when nothing has announced a new
	// notification. It is what makes retries happen.
	Poll time.Duration
	// SendTimeout bounds one attempt to send.
	SendTimeout time.Duration
	// ContentFree sends only poll requests, never message content, so that no push
	// service or relay sees what was published.
	ContentFree bool
	// Backoff says how long to wait after the nth failed attempt (n starts at 1).
	Backoff func(attempt int) time.Duration
	Now     func() time.Time
	Log     *slog.Logger
}

func (c *DispatcherConfig) fill() {
	if c.Workers <= 0 {
		c.Workers = 8
	}
	if c.Batch <= 0 {
		c.Batch = 2 * c.Workers
	}
	if c.Lease <= 0 {
		c.Lease = 2 * time.Minute
	}
	if c.Poll <= 0 {
		c.Poll = 5 * time.Second
	}
	if c.SendTimeout <= 0 {
		c.SendTimeout = 30 * time.Second
	}
	if c.Backoff == nil {
		c.Backoff = DefaultBackoff
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Log == nil {
		c.Log = slog.New(slog.DiscardHandler)
	}
}

// maxRetryDelay caps how long a push service can ask to be left alone, so that
// one wrong header can't park a notification until it expires.
const maxRetryDelay = time.Hour

// DefaultBackoff waits 10 seconds after the first failure and doubles that each
// time, up to half an hour, with a quarter either way so that notifications that
// failed together don't all return together.
func DefaultBackoff(attempt int) time.Duration {
	d := 10 * time.Second
	for i := 1; i < attempt && d < 30*time.Minute; i++ {
		d *= 2
	}
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	jitter := 0.75 + rand.Float64()*0.5
	return time.Duration(float64(d) * jitter)
}

// Dispatcher sends the notifications that publishing queued.
type Dispatcher struct {
	store    Store
	registry *Registry
	access   Access
	cfg      DispatcherConfig
	wake     chan struct{}
}

// NewDispatcher returns a Dispatcher that sends through the registry's senders.
// Before each notification it asks access whether the device's owner may still
// read the topic.
func NewDispatcher(st Store, registry *Registry, access Access, cfg DispatcherConfig) *Dispatcher {
	cfg.fill()
	return &Dispatcher{store: st, registry: registry, access: access, cfg: cfg, wake: make(chan struct{}, 1)}
}

// Wake tells the dispatcher that notifications were queued. It never blocks.
func (d *Dispatcher) Wake() {
	select {
	case d.wake <- struct{}{}:
	default: // one is already pending
	}
}

// Run sends notifications until ctx is cancelled, then waits for the ones being
// sent. A notification interrupted by the shutdown stays queued and is sent after
// its lease runs out, by the next run.
func (d *Dispatcher) Run(ctx context.Context) {
	work := make(chan Job)
	var wg sync.WaitGroup
	for i := 0; i < d.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range work {
				d.send(ctx, job)
			}
		}()
	}
	defer func() { close(work); wg.Wait() }()

	timer := time.NewTimer(0) // look at once: a restart may find notifications waiting
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
		case <-timer.C:
		}
		for {
			n, err := d.dispatch(ctx, work)
			if err != nil {
				if ctx.Err() == nil {
					d.cfg.Log.Error("could not read the push queue", "error", err)
				}
				break
			}
			if n < d.cfg.Batch {
				break // nothing more is due
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(d.cfg.Poll)
	}
}

// dispatch takes one batch from the queue, drops what its owner may no longer
// read, and hands the rest to the workers. It returns how many it took.
func (d *Dispatcher) dispatch(ctx context.Context, work chan<- Job) (int, error) {
	jobs, err := d.store.ClaimJobs(ctx, d.cfg.Now(), d.cfg.Lease, d.cfg.Batch)
	if err != nil {
		return 0, err
	}
	type who struct{ user, topic string }
	checked := map[who]bool{} // one question per owner and topic in a batch
	for _, job := range jobs {
		key := who{job.Device.UserID, job.Topic}
		ok, known := checked[key]
		if !known {
			var err error
			if ok, err = d.access.MayRead(ctx, key.user, key.topic); err != nil {
				if ctx.Err() != nil {
					return len(jobs), nil
				}
				// Not knowing is not a refusal: try again soon, without counting it.
				d.cfg.Log.Error("could not check a device's access", "error", err)
				d.bookkeeping(func(c context.Context) error {
					return d.store.RetryJob(c, job.ID, job.Attempts, d.cfg.Now().Add(30*time.Second))
				})
				continue
			}
			checked[key] = ok
		}
		if !ok {
			// The owner has lost access since the device subscribed. Dropping the
			// subscription drops its queued notifications too.
			d.cfg.Log.Info("dropping a push subscription whose owner may no longer read the topic", "device", job.Device.ID)
			d.bookkeeping(func(c context.Context) error { return d.store.DeleteSubscription(c, job.SubscriptionID) })
			continue
		}
		select {
		case work <- job:
		case <-ctx.Done():
			return len(jobs), nil
		}
	}
	return len(jobs), nil
}

// bookkeeping records an outcome in the store. It is not cut short by a
// shutdown: a notification that was sent must not be sent again because the
// server was stopping when it finished.
func (d *Dispatcher) bookkeeping(f func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f(ctx); err != nil {
		d.cfg.Log.Error("could not record the outcome of a push", "error", err)
	}
}

// send delivers one notification and acts on how that went.
func (d *Dispatcher) send(ctx context.Context, job Job) {
	log := d.cfg.Log.With("device", job.Device.ID, "provider", string(job.Device.Provider))
	sender, ok := d.registry.For(job.Device)
	if !ok {
		// Credentials were removed from the configuration after the device registered.
		log.Warn("no way to send to a device any more; dropping its notification")
		d.bookkeeping(func(c context.Context) error { return d.store.CompleteJob(c, job.ID) })
		return
	}

	now := d.cfg.Now()
	payload, wakeUp := PayloadFor(sender, job.Message, job.SubscriptionID, d.cfg.ContentFree)
	attemptCtx, cancel := context.WithTimeout(ctx, d.cfg.SendTimeout)
	err := sender.Send(attemptCtx, Notification{
		Device: job.Device, SubscriptionID: job.SubscriptionID, Message: job.Message,
		Payload: payload, WakeUp: wakeUp, Expires: job.ExpiresAt, Now: now,
	})
	cancel()

	switch {
	case err == nil:
		d.bookkeeping(func(c context.Context) error { return d.store.CompleteJob(c, job.ID) })
	case ctx.Err() != nil:
		// Interrupted by shutdown: leave it for the lease to return it to the queue.
	default:
		d.failed(log, job, now, classify(err))
	}
}

func (d *Dispatcher) failed(log *slog.Logger, job Job, now time.Time, err *SendError) {
	switch {
	case err.Unregistered:
		log.Info("the push service no longer knows a device; forgetting it", "error", err)
		d.bookkeeping(func(c context.Context) error { return d.store.DeleteDevice(c, job.Device.ID) })
	case err.Permanent:
		log.Warn("a push was refused for good; giving up on it", "error", err)
		d.bookkeeping(func(c context.Context) error { return d.store.CompleteJob(c, job.ID) })
	default:
		attempts := job.Attempts + 1
		delay := d.cfg.Backoff(attempts)
		if err.RetryAfter > delay {
			delay = err.RetryAfter
		}
		if delay > maxRetryDelay {
			delay = maxRetryDelay
		}
		next := now.Add(delay)
		if !next.Before(job.ExpiresAt) {
			log.Warn("a push could not be sent before its message expired; giving up", "attempts", attempts, "error", err)
			d.bookkeeping(func(c context.Context) error { return d.store.CompleteJob(c, job.ID) })
			return
		}
		log.Info("a push failed; will try again", "attempts", attempts, "in", delay.Round(time.Second).String(), "error", err)
		d.bookkeeping(func(c context.Context) error { return d.store.RetryJob(c, job.ID, attempts, next) })
	}
}
