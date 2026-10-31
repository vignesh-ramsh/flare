package store

import (
	"context"
	"fmt"
	"strings"
)

// Settings returns every stored setting whose key starts with prefix, by key.
func (s *SQLite) Settings(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value FROM settings WHERE substr(key, 1, ?) = ?`, len(prefix), prefix)
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("read a setting: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

// PutSetting stores a setting, replacing any value the key had.
func (s *SQLite) PutSetting(ctx context.Context, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("a setting needs a name")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store setting %q: %w", key, err)
	}
	return nil
}

// DeleteSetting removes a setting. A setting that is not there is not an error.
func (s *SQLite) DeleteSetting(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		return fmt.Errorf("delete setting %q: %w", key, err)
	}
	return nil
}
