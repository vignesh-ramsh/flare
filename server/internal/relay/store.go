package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/push"
	"github.com/vignesh-ramsh/flare/server/internal/store"
	"github.com/vignesh-ramsh/flare/server/migrations"
)

// ErrNotFound is returned when a handle isn't there.
var ErrNotFound = errors.New("not found")

// Handle is a push token an app has given the relay.
type Handle struct {
	// Hash is the SHA-256 of the handle, which is how it is looked up; the handle
	// itself is not kept.
	Hash       string
	SecretHash string
	AppID      string
	Platform   string
	Provider   push.Provider
	Token      string
	CreatedAt  time.Time
}

// Store keeps the handles in a database of the relay's own.
type Store struct{ db *sql.DB }

// versionTable is where the relay records which migrations it has applied,
// apart from goose's default so that it can never be confused with a server's.
const versionTable = "relay_db_version"

// OpenStore opens the relay's database at path, creating it if needed.
func OpenStore(ctx context.Context, path string) (*Store, error) {
	sub, err := fs.Sub(migrations.RelayFS, "relay")
	if err != nil {
		return nil, err
	}
	db, err := store.OpenDatabase(ctx, path, sub, versionTable)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Register saves h, replacing the handle of any earlier registration of the same
// token: a token reaches one device, and an app that registers again at every
// launch must not leave a trail of handles.
func (s *Store) Register(ctx context.Context, h Handle) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("register handle: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM handles WHERE provider = ? AND app_id = ? AND token = ?`, string(h.Provider), h.AppID, h.Token); err != nil {
		return fmt.Errorf("replace handle: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO handles (handle_hash, secret_hash, app_id, platform, provider, token, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		h.Hash, h.SecretHash, h.AppID, h.Platform, string(h.Provider), h.Token, h.CreatedAt.Unix()); err != nil {
		return fmt.Errorf("insert handle: %w", err)
	}
	return tx.Commit()
}

// Find returns the handle with the given hash, or ErrNotFound.
func (s *Store) Find(ctx context.Context, hash string) (Handle, error) {
	var h Handle
	var provider string
	var created int64
	err := s.db.QueryRowContext(ctx,
		`SELECT handle_hash, secret_hash, app_id, platform, provider, token, created_at FROM handles WHERE handle_hash = ?`, hash).
		Scan(&h.Hash, &h.SecretHash, &h.AppID, &h.Platform, &provider, &h.Token, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Handle{}, ErrNotFound
	}
	if err != nil {
		return Handle{}, fmt.Errorf("read handle: %w", err)
	}
	h.Provider, h.CreatedAt = push.Provider(provider), time.Unix(created, 0)
	return h, nil
}

// Remove deletes the handle, or returns ErrNotFound.
func (s *Store) Remove(ctx context.Context, hash string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM handles WHERE handle_hash = ?`, hash)
	if err != nil {
		return fmt.Errorf("remove handle: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Woke records that the handle's device was woken at the given time.
func (s *Store) Woke(ctx context.Context, hash string, at time.Time) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE handles SET woken_at = ? WHERE handle_hash = ?`, at.Unix(), hash); err != nil {
		return fmt.Errorf("record wake: %w", err)
	}
	return nil
}

// Count returns how many handles there are.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM handles`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count handles: %w", err)
	}
	return n, nil
}

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }
