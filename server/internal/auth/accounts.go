package auth

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned by Accounts when what was asked for isn't there.
	ErrNotFound = errors.New("not found")
	// ErrExists is returned by Accounts when a user name is already taken.
	ErrExists = errors.New("already exists")
)

// Accounts is where users, tokens and access rules are kept. SQLite implements it
// for a single server; PostgreSQL will for a cluster. Passwords and tokens are
// passed to it already hashed: it never sees a secret.
type Accounts interface {
	// CreateUser adds a user. It returns ErrExists if the name is taken.
	CreateUser(ctx context.Context, u User, passwordHash string) error
	// UserByName and UserByID return the user and their password hash, or
	// ErrNotFound.
	UserByName(ctx context.Context, name string) (User, string, error)
	UserByID(ctx context.Context, id string) (User, string, error)
	ListUsers(ctx context.Context) ([]User, error)
	SetPassword(ctx context.Context, userID, passwordHash string) error
	// SetRole changes what kind of user someone is, or returns ErrNotFound.
	SetRole(ctx context.Context, userID string, role Role) error
	// SetDisabled disables or enables a user, or returns ErrNotFound.
	SetDisabled(ctx context.Context, userID string, disabled bool) error
	// DeleteUser removes the user along with their tokens and access rules.
	DeleteUser(ctx context.Context, userID string) error

	CreateToken(ctx context.Context, t Token, tokenHash string) error
	// TokenByHash returns the token's record and its owner, or ErrNotFound.
	TokenByHash(ctx context.Context, tokenHash string) (Token, User, error)
	// TouchToken records that the token was used at the given time.
	TouchToken(ctx context.Context, tokenID string, at time.Time) error
	ListTokens(ctx context.Context, userID string) ([]Token, error)
	// DeleteToken removes a token, or returns ErrNotFound. With an owner given, only
	// that user's token will do; with "", any.
	DeleteToken(ctx context.Context, tokenID, ownerID string) error

	// GrantAccess adds a rule, replacing the user's existing rule for the same
	// pattern.
	GrantAccess(ctx context.Context, r Rule) error
	// RevokeAccess removes a rule, or returns ErrNotFound. userID "" means the rule
	// for everyone.
	RevokeAccess(ctx context.Context, userID, pattern string) error
	// RulesFor returns the rules that apply to a user: their own and everyone's.
	// For userID "" (an anonymous visitor) that is only everyone's.
	RulesFor(ctx context.Context, userID string) ([]Rule, error)
	// ListRules returns every rule.
	ListRules(ctx context.Context) ([]Rule, error)
}
