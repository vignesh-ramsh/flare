package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/theme"
)

var _ theme.Store = (*SQLite)(nil)

func (s *SQLite) Themes(ctx context.Context) ([]theme.Stored, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, brightness, colors, created_at, updated_at FROM themes ORDER BY name_key`)
	if err != nil {
		return nil, fmt.Errorf("list themes: %w", err)
	}
	defer rows.Close()
	out := []theme.Stored{}
	for rows.Next() {
		var t theme.Stored
		var colors string
		var created, updated int64
		if err := rows.Scan(&t.Name, &t.Brightness, &colors, &created, &updated); err != nil {
			return nil, fmt.Errorf("read a theme: %w", err)
		}
		if err := json.Unmarshal([]byte(colors), &t.Colors); err != nil {
			return nil, fmt.Errorf("read the colours of theme %q: %w", t.Name, err)
		}
		t.CreatedAt, t.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *SQLite) CreateTheme(ctx context.Context, t theme.Theme, now time.Time, max int) error {
	colors, err := json.Marshal(t.Colors)
	if err != nil {
		return fmt.Errorf("encode theme colours: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create theme: %w", err)
	}
	defer tx.Rollback()

	var held int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM themes`).Scan(&held); err != nil {
		return fmt.Errorf("count themes: %w", err)
	}
	if held >= max {
		return theme.ErrLimit
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO themes (name_key, name, brightness, colors, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		theme.Key(t.Name), t.Name, t.Brightness, string(colors), unix(now), unix(now))
	switch {
	case isUnique(err):
		return theme.ErrExists
	case err != nil:
		return fmt.Errorf("insert theme: %w", err)
	}
	return tx.Commit()
}

func (s *SQLite) ReplaceTheme(ctx context.Context, t theme.Theme, now time.Time) error {
	colors, err := json.Marshal(t.Colors)
	if err != nil {
		return fmt.Errorf("encode theme colours: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE themes SET name = ?, brightness = ?, colors = ?, updated_at = ? WHERE name_key = ?`,
		t.Name, t.Brightness, string(colors), unix(now), theme.Key(t.Name))
	if err != nil {
		return fmt.Errorf("replace theme: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return theme.ErrNotFound
	}
	return nil
}

func (s *SQLite) DeleteTheme(ctx context.Context, nameKey string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM themes WHERE name_key = ?`, nameKey)
	if err != nil {
		return fmt.Errorf("delete theme: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return theme.ErrNotFound
	}
	return nil
}
