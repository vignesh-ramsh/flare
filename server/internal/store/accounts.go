package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
)

var _ auth.Accounts = (*SQLite)(nil)

// isUnique reports whether err is a violation of a UNIQUE or PRIMARY KEY
// constraint.
func isUnique(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && (e.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || e.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY)
}

func unix(t time.Time) int64 { return t.Unix() }

func optionalTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0)
	return &t
}

func nullableTime(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}

// --- users -------------------------------------------------------------------

func (s *SQLite) CreateUser(ctx context.Context, u auth.User, passwordHash string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Name, passwordHash, string(u.Role), unix(u.CreatedAt))
	switch {
	case isUnique(err):
		return auth.ErrExists
	case err != nil:
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

func scanUser(row interface{ Scan(...any) error }) (auth.User, string, error) {
	var (
		u        auth.User
		role     string
		created  int64
		hash     string
		disabled int
	)
	if err := row.Scan(&u.ID, &u.Name, &hash, &role, &created, &disabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.User{}, "", auth.ErrNotFound
		}
		return auth.User{}, "", fmt.Errorf("read user: %w", err)
	}
	u.Role, u.CreatedAt, u.Disabled = auth.Role(role), time.Unix(created, 0), disabled != 0
	return u, hash, nil
}

const userColumns = `id, name, password_hash, role, created_at, disabled`

func (s *SQLite) UserByName(ctx context.Context, name string) (auth.User, string, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE name = ?`, name))
}

func (s *SQLite) UserByID(ctx context.Context, id string) (auth.User, string, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

func (s *SQLite) ListUsers(ctx context.Context) ([]auth.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []auth.User
	for rows.Next() {
		u, _, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *SQLite) SetPassword(ctx context.Context, userID, passwordHash string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID)
	return expectOne(res, err, "set password")
}

func (s *SQLite) SetRole(ctx context.Context, userID string, role auth.Role) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, string(role), userID)
	return expectOne(res, err, "set role")
}

func (s *SQLite) SetDisabled(ctx context.Context, userID string, disabled bool) error {
	flag := 0
	if disabled {
		flag = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET disabled = ? WHERE id = ?`, flag, userID)
	return expectOne(res, err, "set disabled")
}

func (s *SQLite) DeleteUser(ctx context.Context, userID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	return expectOne(res, err, "delete user")
}

// expectOne turns the result of an UPDATE or DELETE into ErrNotFound if it
// changed nothing.
func expectOne(res sql.Result, err error, what string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return auth.ErrNotFound
	}
	return nil
}

// --- tokens ------------------------------------------------------------------

func (s *SQLite) CreateToken(ctx context.Context, t auth.Token, tokenHash string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (id, user_id, token_hash, label, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, tokenHash, t.Label, unix(t.CreatedAt), nullableTime(t.ExpiresAt))
	if err != nil {
		return fmt.Errorf("create token: %w", err)
	}
	return nil
}

const tokenColumns = `t.id, t.user_id, t.label, t.created_at, t.expires_at, t.last_used_at`

func scanToken(row interface{ Scan(...any) error }, extra ...any) (auth.Token, error) {
	var (
		t       auth.Token
		created int64
		expires sql.NullInt64
		used    sql.NullInt64
	)
	dest := append([]any{&t.ID, &t.UserID, &t.Label, &created, &expires, &used}, extra...)
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.Token{}, auth.ErrNotFound
		}
		return auth.Token{}, fmt.Errorf("read token: %w", err)
	}
	t.CreatedAt, t.ExpiresAt, t.LastUsedAt = time.Unix(created, 0), optionalTime(expires), optionalTime(used)
	return t, nil
}

func (s *SQLite) TokenByHash(ctx context.Context, tokenHash string) (auth.Token, auth.User, error) {
	var (
		u        auth.User
		role     string
		created  int64
		disabled int
	)
	t, err := scanToken(s.db.QueryRowContext(ctx,
		`SELECT `+tokenColumns+`, u.name, u.role, u.created_at, u.disabled
		   FROM tokens t JOIN users u ON u.id = t.user_id
		  WHERE t.token_hash = ?`, tokenHash), &u.Name, &role, &created, &disabled)
	if err != nil {
		return auth.Token{}, auth.User{}, err
	}
	u.ID, u.Role, u.CreatedAt, u.Disabled = t.UserID, auth.Role(role), time.Unix(created, 0), disabled != 0
	return t, u, nil
}

func (s *SQLite) TouchToken(ctx context.Context, tokenID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tokens SET last_used_at = ? WHERE id = ?`, unix(at), tokenID)
	if err != nil {
		return fmt.Errorf("record token use: %w", err)
	}
	return nil
}

func (s *SQLite) ListTokens(ctx context.Context, userID string) ([]auth.Token, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+tokenColumns+` FROM tokens t WHERE t.user_id = ? ORDER BY t.created_at, t.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()
	var out []auth.Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteToken(ctx context.Context, tokenID, ownerID string) error {
	query, args := `DELETE FROM tokens WHERE id = ?`, []any{tokenID}
	if ownerID != "" {
		query, args = `DELETE FROM tokens WHERE id = ? AND user_id = ?`, []any{tokenID, ownerID}
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	return expectOne(res, err, "delete token")
}

// --- access rules ------------------------------------------------------------

func nullableUser(id string) sql.NullString { return sql.NullString{String: id, Valid: id != ""} }

func (s *SQLite) GrantAccess(ctx context.Context, r auth.Rule) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("grant access: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM acl WHERE COALESCE(user_id, '') = ? AND pattern = ?`, r.UserID, r.Pattern); err != nil {
		return fmt.Errorf("grant access: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO acl (user_id, pattern, access) VALUES (?, ?, ?)`,
		nullableUser(r.UserID), r.Pattern, string(r.Access)); err != nil {
		return fmt.Errorf("grant access: %w", err)
	}
	return tx.Commit()
}

func (s *SQLite) RevokeAccess(ctx context.Context, userID, pattern string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM acl WHERE COALESCE(user_id, '') = ? AND pattern = ?`, userID, pattern)
	return expectOne(res, err, "revoke access")
}

func (s *SQLite) queryRules(ctx context.Context, query string, args ...any) ([]auth.Rule, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read access rules: %w", err)
	}
	defer rows.Close()
	var out []auth.Rule
	for rows.Next() {
		var (
			r      auth.Rule
			user   sql.NullString
			access string
		)
		if err := rows.Scan(&user, &r.Pattern, &access); err != nil {
			return nil, fmt.Errorf("read access rule: %w", err)
		}
		r.UserID, r.Access = user.String, auth.Access(access)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLite) RulesFor(ctx context.Context, userID string) ([]auth.Rule, error) {
	return s.queryRules(ctx,
		`SELECT user_id, pattern, access FROM acl WHERE user_id IS NULL OR user_id = ? ORDER BY pattern, id`, userID)
}

func (s *SQLite) ListRules(ctx context.Context) ([]auth.Rule, error) {
	return s.queryRules(ctx, `SELECT user_id, pattern, access FROM acl ORDER BY user_id IS NOT NULL, user_id, pattern`)
}
