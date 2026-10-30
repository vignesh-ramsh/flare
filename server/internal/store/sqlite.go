package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, so the binary needs no C toolchain

	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/migrations"
)

// SQLite is a Store backed by one SQLite database file.
type SQLite struct {
	db *sql.DB
}

var _ Store = (*SQLite)(nil)

// OpenSQLite opens the database at path, creating the file and its folder if
// needed, and brings its schema up to date.
func OpenSQLite(ctx context.Context, path string) (*SQLite, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create database folder: %w", err)
		}
	}

	// journal_mode=WAL lets readers run while a write happens. synchronous=FULL
	// makes each commit durable before Insert returns, so a message the server
	// acknowledged survives a crash or power loss. busy_timeout makes a writer
	// wait for another instead of failing.
	query := strings.Join([]string{
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(FULL)",
		"_pragma=busy_timeout(5000)",
	}, "&")
	db, err := sql.Open("sqlite", "file:"+escapePath(path)+"?"+query)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(16)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLite{db: db}, nil
}

// escapePath makes path safe to use inside a SQLite "file:" URI.
func escapePath(path string) string {
	return strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
}

func migrate(ctx context.Context, db *sql.DB) error {
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("prepare migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func (s *SQLite) Insert(ctx context.Context, m message.Message) error {
	tags := m.Tags
	if tags == nil {
		tags = []string{}
	}
	encoded, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO messages (id, topic, time, expires_at, priority, title, body, tags, click)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Topic, m.Time, m.Expires, m.Priority, m.Title, m.Message, string(encoded), m.Click)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

func (s *SQLite) After(ctx context.Context, topics []string, afterID string, now time.Time, limit int) ([]message.Message, error) {
	if len(topics) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(topics)), ",")
	args := make([]any, 0, len(topics)+3)
	for _, topic := range topics {
		args = append(args, topic)
	}
	args = append(args, afterID, now.Unix(), limit)

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, topic, time, expires_at, priority, title, body, tags, click
		   FROM messages
		  WHERE topic IN (`+placeholders+`) AND id > ? AND expires_at > ?
		  ORDER BY id
		  LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var out []message.Message
	for rows.Next() {
		m := message.Message{Event: message.EventMessage}
		var tags string
		if err := rows.Scan(&m.ID, &m.Topic, &m.Time, &m.Expires, &m.Priority, &m.Title, &m.Message, &tags, &m.Click); err != nil {
			return nil, fmt.Errorf("read message: %w", err)
		}
		if err := json.Unmarshal([]byte(tags), &m.Tags); err != nil {
			return nil, fmt.Errorf("read tags of message %s: %w", m.ID, err)
		}
		if len(m.Tags) == 0 {
			m.Tags = nil
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("delete expired messages: %w", err)
	}
	return res.RowsAffected()
}

func (s *SQLite) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *SQLite) Close() error { return s.db.Close() }
