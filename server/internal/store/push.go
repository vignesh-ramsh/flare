package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/push"
)

var _ push.Store = (*SQLite)(nil)

// newSubscriptionID returns an opaque, unguessable identifier.
func newSubscriptionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

const deviceColumns = `id, secret_hash, COALESCE(user_id, ''), app_id, platform, provider, target,
	p256dh, auth_secret, relay_url, app_version, created_at, seen_at`

func scanDevice(row interface{ Scan(...any) error }) (push.Device, error) {
	var d push.Device
	var provider string
	var created, seen int64
	err := row.Scan(&d.ID, &d.SecretHash, &d.UserID, &d.AppID, &d.Platform, &provider, &d.Target,
		&d.Keys.P256DH, &d.Keys.Auth, &d.RelayURL, &d.AppVersion, &created, &seen)
	d.Provider = push.Provider(provider)
	d.CreatedAt, d.SeenAt = time.Unix(created, 0), time.Unix(seen, 0)
	return d, err
}

func (s *SQLite) RegisterDevice(ctx context.Context, d push.Device, max int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("register device: %w", err)
	}
	defer tx.Rollback()

	// The same target registering again is the same device coming back, perhaps
	// without the secret it was given: the new registration replaces the old.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM devices WHERE provider = ? AND relay_url = ? AND target = ?`,
		string(d.Provider), d.RelayURL, d.Target); err != nil {
		return fmt.Errorf("replace device: %w", err)
	}
	var owned int
	var err2 error
	if d.UserID == "" {
		err2 = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE user_id IS NULL`).Scan(&owned)
	} else {
		err2 = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE user_id = ?`, d.UserID).Scan(&owned)
	}
	if err2 != nil {
		return fmt.Errorf("count devices: %w", err2)
	}
	if owned >= max {
		return push.ErrTooManyDevices
	}
	var owner any
	if d.UserID != "" {
		owner = d.UserID
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO devices (id, secret_hash, user_id, app_id, platform, provider, target,
		                      p256dh, auth_secret, relay_url, app_version, created_at, seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.SecretHash, owner, d.AppID, d.Platform, string(d.Provider), d.Target,
		d.Keys.P256DH, d.Keys.Auth, d.RelayURL, d.AppVersion, d.CreatedAt.Unix(), d.SeenAt.Unix()); err != nil {
		return fmt.Errorf("insert device: %w", err)
	}
	return tx.Commit()
}

func (s *SQLite) Device(ctx context.Context, id string) (push.Device, error) {
	d, err := scanDevice(s.db.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return push.Device{}, push.ErrNotFound
	}
	if err != nil {
		return push.Device{}, fmt.Errorf("read device: %w", err)
	}
	return d, nil
}

func (s *SQLite) DeleteDevice(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return push.ErrNotFound
	}
	return nil
}

func (s *SQLite) Touch(ctx context.Context, id string, at time.Time) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE devices SET seen_at = ? WHERE id = ?`, at.Unix(), id); err != nil {
		return fmt.Errorf("touch device: %w", err)
	}
	return nil
}

func (s *SQLite) SetSubscriptions(ctx context.Context, deviceID string, topics []string, now time.Time) ([]push.Subscription, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("set subscriptions: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE id = ?`, deviceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("set subscriptions: %w", err)
	}
	if exists == 0 {
		return nil, push.ErrNotFound
	}

	have := map[string]string{} // topic -> subscription ID
	rows, err := tx.QueryContext(ctx, `SELECT topic, id FROM push_subscriptions WHERE device_id = ?`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("read subscriptions: %w", err)
	}
	for rows.Next() {
		var topic, id string
		if err := rows.Scan(&topic, &id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read subscriptions: %w", err)
		}
		have[topic] = id
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read subscriptions: %w", err)
	}
	rows.Close()

	want := make(map[string]bool, len(topics))
	for _, t := range topics {
		want[t] = true
	}
	for topic, id := range have {
		if !want[topic] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, id); err != nil {
				return nil, fmt.Errorf("remove subscription: %w", err)
			}
		}
	}
	out := make([]push.Subscription, 0, len(topics))
	for _, topic := range topics {
		id, ok := have[topic]
		if !ok {
			id = newSubscriptionID()
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO push_subscriptions (id, device_id, topic, created_at) VALUES (?, ?, ?, ?)`,
				id, deviceID, topic, now.Unix()); err != nil {
				return nil, fmt.Errorf("add subscription: %w", err)
			}
		}
		out = append(out, push.Subscription{ID: id, DeviceID: deviceID, Topic: topic})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("set subscriptions: %w", err)
	}
	return out, nil
}

func (s *SQLite) DeleteSubscription(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	return nil
}

func (s *SQLite) ClaimJobs(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]push.Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("claim jobs: %w", err)
	}
	defer tx.Rollback()

	// Moving next_attempt_at forward is what claims a job: nobody else will pick
	// it up before the lease runs out.
	rows, err := tx.QueryContext(ctx,
		`UPDATE push_jobs SET next_attempt_at = ?
		  WHERE id IN (SELECT id FROM push_jobs WHERE next_attempt_at <= ? AND expires_at > ? ORDER BY id LIMIT ?)
		RETURNING id`, now.Add(lease).Unix(), now.Unix(), now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("claim jobs: %w", err)
	}
	var ids []any
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("claim jobs: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("claim jobs: %w", err)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, tx.Commit()
	}

	in := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	jobRows, err := tx.QueryContext(ctx,
		`SELECT j.id, j.subscription_id, j.attempts, j.expires_at, s.topic,
		        d.id, d.secret_hash, COALESCE(d.user_id, ''), d.app_id, d.platform, d.provider, d.target,
		        d.p256dh, d.auth_secret, d.relay_url, d.app_version, d.created_at, d.seen_at,
		        m.id, m.time, m.expires_at, m.priority, m.title, m.body, m.tags, m.click
		   FROM push_jobs j
		   JOIN push_subscriptions s ON s.id = j.subscription_id
		   JOIN devices d ON d.id = j.device_id
		   JOIN messages m ON m.id = j.message_id
		  WHERE j.id IN (`+in+`)
		  ORDER BY j.id`, ids...)
	if err != nil {
		return nil, fmt.Errorf("read jobs: %w", err)
	}
	defer jobRows.Close()
	var jobs []push.Job
	for jobRows.Next() {
		var j push.Job
		var expires, created, seen int64
		var provider, tags string
		d := &j.Device
		m := &j.Message
		if err := jobRows.Scan(&j.ID, &j.SubscriptionID, &j.Attempts, &expires, &j.Topic,
			&d.ID, &d.SecretHash, &d.UserID, &d.AppID, &d.Platform, &provider, &d.Target,
			&d.Keys.P256DH, &d.Keys.Auth, &d.RelayURL, &d.AppVersion, &created, &seen,
			&m.ID, &m.Time, &m.Expires, &m.Priority, &m.Title, &m.Message, &tags, &m.Click); err != nil {
			return nil, fmt.Errorf("read job: %w", err)
		}
		j.ExpiresAt = time.Unix(expires, 0)
		d.Provider = push.Provider(provider)
		d.CreatedAt, d.SeenAt = time.Unix(created, 0), time.Unix(seen, 0)
		m.Event, m.Topic = message.EventMessage, j.Topic
		if m.Tags, err = decodeTags(tags); err != nil {
			return nil, fmt.Errorf("read tags of message %s: %w", m.ID, err)
		}
		jobs = append(jobs, j)
	}
	if err := jobRows.Err(); err != nil {
		return nil, fmt.Errorf("read jobs: %w", err)
	}
	jobRows.Close()
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("claim jobs: %w", err)
	}
	return jobs, nil
}

func (s *SQLite) CompleteJob(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM push_jobs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	return nil
}

func (s *SQLite) RetryJob(ctx context.Context, id int64, attempts int, next time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE push_jobs SET attempts = ?, next_attempt_at = ? WHERE id = ?`, attempts, next.Unix(), id); err != nil {
		return fmt.Errorf("retry job: %w", err)
	}
	return nil
}

// PendingJobs returns how many notifications are waiting to be sent, due or not.
func (s *SQLite) PendingJobs(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_jobs`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count jobs: %w", err)
	}
	return n, nil
}

func (s *SQLite) DeleteExpiredJobs(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM push_jobs WHERE expires_at <= ? OR message_id NOT IN (SELECT id FROM messages)`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("delete expired jobs: %w", err)
	}
	return res.RowsAffected()
}

func (s *SQLite) Setting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting: %w", err)
	}
	return v, true, nil
}

func (s *SQLite) SetSettingIfAbsent(ctx context.Context, key, value string) (string, error) {
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`, key, value); err != nil {
		return "", fmt.Errorf("save setting: %w", err)
	}
	v, _, err := s.Setting(ctx, key)
	return v, err
}
