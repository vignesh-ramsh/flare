// Package realtime is the heart of delivery: it stores each published message,
// hands it to everyone listening, and lets a subscriber that was offline catch
// up without missing or repeating anything.
package realtime

import (
	"context"
	"sync"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/broker"
	"github.com/vignesh-ramsh/flare/server/internal/id"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/store"
)

// Options configure a Hub. Zero values get sensible defaults.
type Options struct {
	// TTL is how long a message stays available for catching up.
	TTL time.Duration
	// MaxMessageSize is the largest message body, in bytes.
	MaxMessageSize int
	// Buffer is how many live messages may wait for a slow subscriber before it
	// is dropped as lagging.
	Buffer int
	// PageSize is how many stored messages are fetched at a time when catching up.
	PageSize int
	// Now returns the current time. Tests replace it.
	Now func() time.Time
}

func (o *Options) fill() {
	if o.TTL <= 0 {
		o.TTL = 24 * time.Hour
	}
	if o.MaxMessageSize <= 0 {
		o.MaxMessageSize = 4096
	}
	if o.Buffer <= 0 {
		o.Buffer = 256
	}
	if o.PageSize <= 0 {
		o.PageSize = 500
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Hub publishes messages and serves subscribers.
//
// The guarantee it provides: once Publish returns, the message is stored, and
// every subscriber that is connected, or that connects later and asks for it,
// receives it, in ID order and exactly once per connection.
//
// That rests on one rule. Publish assigns an ID, stores the message and hands it
// to the broker while holding a lock, so commit order, ID order and delivery
// order are all the same. A subscriber then needs only a single cursor.
type Hub struct {
	store  store.Store
	broker broker.Broker
	opt    Options

	mu  sync.Mutex // serialises Publish
	gen id.Generator
}

// NewHub returns a Hub that stores messages in st and delivers through br.
func NewHub(st store.Store, br broker.Broker, opt Options) *Hub {
	opt.fill()
	return &Hub{store: st, broker: br, opt: opt}
}

// Publish validates d, stores it, and delivers it to live subscribers. A refused
// draft is reported as a *message.Problem and nothing is stored or delivered.
func (h *Hub) Publish(ctx context.Context, d message.Draft) (message.Message, error) {
	d.Normalize()
	if err := d.Validate(h.opt.MaxMessageSize); err != nil {
		return message.Message{}, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.opt.Now()
	m := message.Message{
		ID:       h.gen.New(now),
		Time:     now.Unix(),
		Expires:  now.Add(h.opt.TTL).Unix(),
		Event:    message.EventMessage,
		Topic:    d.Topic,
		Title:    d.Title,
		Message:  d.Body,
		Priority: d.Priority,
		Tags:     d.Tags,
		Click:    d.Click,
	}
	if err := h.store.Insert(ctx, m); err != nil {
		return message.Message{}, err
	}
	h.broker.Publish(m)
	return m, nil
}

// Checkpoint returns an ID that sorts before every message published from now
// on. A subscriber that has seen no message yet can resume from it. IDs resolve
// to a millisecond, so it may also sort before a message published just before
// the call; a subscriber resuming from it can therefore see that one again.
func (h *Hub) Checkpoint() string { return id.Boundary(h.opt.Now()) }

// Handler receives what a subscription produces. Only Message is required.
type Handler struct {
	// Open is called once the subscription is established: from then on no
	// message published will be missed. It runs before any message is delivered.
	Open func() error
	// Message is called for each message, in order.
	Message func(message.Message) error
	// Heartbeat is called each interval while nothing else is happening, to let
	// the connection show it is alive.
	Heartbeat func() error
}

// Follow delivers messages on topics to h, starting at c, until ctx is done
// (which returns nil) or something goes wrong: a Handler error, a store error,
// or the subscriber falling too far behind (broker.ErrLagged), after which it
// should reconnect and resume from the last message it received.
func (h *Hub) Follow(ctx context.Context, topics []string, c Cursor, interval time.Duration, hd Handler) error {
	// Listen first, then read the backlog. A message published in between shows
	// up in both, and the "after last" check below drops the repeat.
	sub := h.broker.Subscribe(topics, h.opt.Buffer)
	defer sub.Close()

	if hd.Open != nil {
		if err := hd.Open(); err != nil {
			return err
		}
	}

	last := c.After
	if !c.Live {
		var err error
		if last, err = h.replay(ctx, topics, c.After, hd.Message); err != nil {
			return err
		}
	}

	var beat <-chan time.Time
	if interval > 0 && hd.Heartbeat != nil {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		beat = ticker.C
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-beat:
			if err := hd.Heartbeat(); err != nil {
				return err
			}
		case m, ok := <-sub.C():
			if !ok {
				return sub.Err()
			}
			if m.ID <= last {
				continue // already sent as part of the backlog
			}
			if err := hd.Message(m); err != nil {
				return err
			}
			last = m.ID
		}
	}
}

// Replay delivers the cached messages on topics that sort after the given ID, in
// order, and returns. It is the whole of a "poll" request.
func (h *Hub) Replay(ctx context.Context, topics []string, after string, emit func(message.Message) error) error {
	_, err := h.replay(ctx, topics, after, emit)
	return err
}

// replay reads the backlog a page at a time, so a large cache never has to fit
// in memory, and returns the ID of the last message it delivered (or after, if
// there was none).
func (h *Hub) replay(ctx context.Context, topics []string, after string, emit func(message.Message) error) (string, error) {
	for {
		page, err := h.store.After(ctx, topics, after, h.opt.Now(), h.opt.PageSize)
		if err != nil {
			return after, err
		}
		for _, m := range page {
			if err := emit(m); err != nil {
				return after, err
			}
			after = m.ID
		}
		if len(page) < h.opt.PageSize {
			return after, nil
		}
	}
}

// Ready reports whether the hub can currently store messages.
func (h *Hub) Ready(ctx context.Context) error { return h.store.Ping(ctx) }

// Now returns the hub's current time.
func (h *Hub) Now() time.Time { return h.opt.Now() }
