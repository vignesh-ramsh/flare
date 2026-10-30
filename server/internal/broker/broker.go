// Package broker fans a published message out to everyone currently listening
// to its topic.
package broker

import (
	"errors"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

var (
	// ErrLagged is why a subscription ended when its listener fell too far
	// behind. The listener should reconnect and catch up from its last message.
	ErrLagged = errors.New("subscriber fell behind")

	// ErrClosed is why a subscription ended when the broker shut down.
	ErrClosed = errors.New("broker closed")
)

// Broker delivers messages to live subscribers. It does not store anything: a
// subscriber that misses a message catches up from the store afterwards. The
// in-process implementation serves a single node; a NATS-backed one will serve a
// cluster.
type Broker interface {
	// Publish hands m to every subscription on m.Topic. It never blocks: a
	// subscriber with a full buffer is ended with ErrLagged instead.
	Publish(m message.Message)

	// Subscribe starts listening to topics. Up to buffer messages wait for a slow
	// reader before it counts as lagging.
	Subscribe(topics []string, buffer int) Subscription

	// Close ends every subscription with ErrClosed.
	Close()
}

// Subscription is a live feed of messages.
type Subscription interface {
	// C delivers messages in publish order. It is closed when the subscription
	// ends.
	C() <-chan message.Message

	// Err says why C was closed: ErrLagged, ErrClosed, or nil if the listener
	// called Close itself. It is only meaningful once C is closed.
	Err() error

	// Close ends the subscription. It is safe to call more than once.
	Close()
}
