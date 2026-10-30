// Package store persists messages for the duration of the cache.
package store

import (
	"context"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

// Store keeps messages so that a subscriber that was offline can catch up.
// SQLite implements it for a single server; PostgreSQL will for a cluster.
type Store interface {
	// Insert saves m. It returns only once the message is durably stored.
	Insert(ctx context.Context, m message.Message) error

	// After returns up to limit unexpired messages from any of topics whose ID
	// sorts after afterID (use "" for all of them), oldest first.
	After(ctx context.Context, topics []string, afterID string, now time.Time, limit int) ([]message.Message, error)

	// DeleteExpired removes messages that expired at or before now and returns
	// how many it removed.
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)

	// Ping checks that the database is reachable.
	Ping(ctx context.Context) error

	Close() error
}
