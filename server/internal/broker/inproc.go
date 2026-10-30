package broker

import (
	"sync"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

// InProc is a Broker for a single process.
type InProc struct {
	mu     sync.RWMutex
	topics map[string]map[*subscription]struct{}
	closed bool
}

var _ Broker = (*InProc)(nil)

// NewInProc returns an empty broker.
func NewInProc() *InProc {
	return &InProc{topics: map[string]map[*subscription]struct{}{}}
}

type subscription struct {
	broker *InProc
	topics []string
	ch     chan message.Message
	once   sync.Once
	err    error // written before ch is closed, so readers that saw the close see it
}

func (b *InProc) Publish(m message.Message) {
	var lagging []*subscription
	b.mu.RLock()
	for s := range b.topics[m.Topic] {
		select {
		case s.ch <- m:
		default:
			lagging = append(lagging, s)
		}
	}
	b.mu.RUnlock()
	for _, s := range lagging {
		s.end(ErrLagged)
	}
}

func (b *InProc) Subscribe(topics []string, buffer int) Subscription {
	s := &subscription{
		broker: b,
		topics: append([]string(nil), topics...),
		ch:     make(chan message.Message, max(buffer, 1)),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		s.err = ErrClosed
		close(s.ch)
		s.once.Do(func() {})
		return s
	}
	for _, topic := range s.topics {
		if b.topics[topic] == nil {
			b.topics[topic] = map[*subscription]struct{}{}
		}
		b.topics[topic][s] = struct{}{}
	}
	return s
}

func (b *InProc) Close() {
	b.mu.Lock()
	b.closed = true
	var all []*subscription
	for _, subs := range b.topics {
		for s := range subs {
			all = append(all, s)
		}
	}
	b.mu.Unlock()
	for _, s := range all {
		s.end(ErrClosed)
	}
}

func (s *subscription) C() <-chan message.Message { return s.ch }
func (s *subscription) Err() error                { return s.err }
func (s *subscription) Close()                    { s.end(nil) }

// end removes the subscription from the broker and closes its channel. Senders
// hold the read lock, so once this has taken the write lock none is mid-send and
// closing the channel is safe.
func (s *subscription) end(err error) {
	s.once.Do(func() {
		b := s.broker
		b.mu.Lock()
		for _, topic := range s.topics {
			delete(b.topics[topic], s)
			if len(b.topics[topic]) == 0 {
				delete(b.topics, topic)
			}
		}
		b.mu.Unlock()
		s.err = err
		close(s.ch)
	})
}
