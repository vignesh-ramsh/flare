package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// ErrTooManyTokens is returned when a user already has as many tokens as are
// allowed.
var ErrTooManyTokens = errors.New("too many tokens")

// InputError is returned when what the caller supplied is not acceptable and the
// caller could fix it, as opposed to something going wrong on the server.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// ServiceConfig tunes a Service. Zero values get sensible defaults.
type ServiceConfig struct {
	// Default is what anyone may do on a topic that no rule covers.
	Default Access
	// Proxies are the reverse proxies whose X-Forwarded-For is believed.
	Proxies ProxyTrust
	// HashParams are the Argon2id cost settings for new passwords.
	HashParams Params
	// MaxHashes limits how many passwords are hashed or checked at once.
	MaxHashes int
	// FailureLimit and FailureWindow throttle failed logins per client address.
	FailureLimit  int
	FailureWindow time.Duration
	// MaxTokensPerUser limits how many tokens one user can have.
	MaxTokensPerUser int
	// Now returns the current time. Tests replace it.
	Now func() time.Time
}

func (c *ServiceConfig) fill() {
	if c.HashParams == (Params{}) {
		c.HashParams = DefaultParams
	}
	if c.MaxHashes <= 0 {
		c.MaxHashes = 4
	}
	if c.FailureLimit <= 0 {
		c.FailureLimit = 10
	}
	if c.FailureWindow <= 0 {
		c.FailureWindow = 10 * time.Minute
	}
	if c.MaxTokensPerUser <= 0 {
		c.MaxTokensPerUser = 50
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// touchInterval is how often a token's "last used" time is updated, so that
// using a token doesn't cost a database write every time.
const touchInterval = time.Minute

// Service authenticates requests, decides what they may do, and manages users,
// tokens and access rules.
type Service struct {
	acc      Accounts
	cfg      ServiceConfig
	hasher   *Hasher
	throttle *Throttle

	// def is what is allowed where no rule says otherwise. An administrator may change it
	// while the server runs.
	def atomic.Pointer[Access]

	mu      sync.Mutex
	touched map[string]time.Time // token ID -> when its last-used time was last written
}

var _ Authorizer = (*Service)(nil)

// NewService returns a Service that keeps its accounts in acc.
func NewService(acc Accounts, cfg ServiceConfig) *Service {
	cfg.fill()
	th := NewThrottle(cfg.FailureLimit, cfg.FailureWindow)
	th.now = cfg.Now
	s := &Service{
		acc: acc, cfg: cfg,
		hasher:   NewHasher(cfg.HashParams, cfg.MaxHashes),
		throttle: th,
		touched:  map[string]time.Time{},
	}
	def := cfg.Default
	s.def.Store(&def)
	return s
}

// Default returns what the server allows where no rule says otherwise.
func (s *Service) Default() Access { return *s.def.Load() }

// SetDefault changes what the server allows where no rule says otherwise, from the next
// request on. Connections that are open notice within the interval at which they re-check.
func (s *Service) SetDefault(a Access) { s.def.Store(&a) }

// ClientIP returns the address a request really comes from.
func (s *Service) ClientIP(r *http.Request) string { return s.cfg.Proxies.ClientIP(r) }

// --- authenticating ----------------------------------------------------------

// Authenticate implements Authorizer.
func (s *Service) Authenticate(r *http.Request) (Identity, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return Identity{}, nil
	}
	return s.attempt(r.Context(), s.ClientIP(r), func() (Identity, error) {
		scheme, credentials, _ := strings.Cut(header, " ")
		credentials = strings.TrimSpace(credentials)
		switch strings.ToLower(scheme) {
		case "bearer":
			return s.byToken(r.Context(), credentials)
		case "basic":
			return s.byBasic(r.Context(), credentials)
		}
		return Identity{}, ErrBadCredentials
	})
}

// AuthenticateToken implements Authorizer.
func (s *Service) AuthenticateToken(ctx context.Context, token, clientIP string) (Identity, error) {
	return s.attempt(ctx, clientIP, func() (Identity, error) { return s.byToken(ctx, token) })
}

// Login authenticates a request that must carry a user name and password, as
// logging in to get a token does. A token won't do: it would let whoever stole one
// make more, outliving its revocation.
func (s *Service) Login(r *http.Request) (Identity, error) {
	scheme, credentials, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "basic") {
		return Identity{}, fmt.Errorf("%w: logging in needs a user name and password (HTTP Basic)", ErrUnauthorized)
	}
	return s.attempt(r.Context(), s.ClientIP(r), func() (Identity, error) {
		return s.byBasic(r.Context(), strings.TrimSpace(credentials))
	})
}

// attempt runs one authentication attempt under the failure throttle. Whoever has
// failed too often is refused without being tried, and a wrong credential counts
// as a failure.
func (s *Service) attempt(ctx context.Context, ip string, try func() (Identity, error)) (Identity, error) {
	key := ThrottleKey(ip)
	if wait, ok := s.throttle.Check(key); !ok {
		return Identity{}, &ThrottledError{RetryAfter: wait}
	}
	id, err := try()
	if errors.Is(err, ErrBadCredentials) {
		s.throttle.Fail(key)
	}
	return id, err
}

func (s *Service) byBasic(ctx context.Context, encoded string) (Identity, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Identity{}, ErrBadCredentials
	}
	name, password, ok := strings.Cut(string(raw), ":")
	if !ok || name == "" || len(name) > 64 || len(password) > MaxPasswordLength {
		return Identity{}, ErrBadCredentials // too long to be real: don't spend a hash on it
	}
	return s.byPassword(ctx, strings.ToLower(name), password)
}

func (s *Service) byPassword(ctx context.Context, name, password string) (Identity, error) {
	user, hash, err := s.acc.UserByName(ctx, name)
	if errors.Is(err, ErrNotFound) {
		// Do the same work as for a real user, so that how long this takes doesn't
		// show whether the name exists.
		if _, err := s.hasher.Verify(ctx, password, s.hasher.Dummy(ctx)); err != nil && ctx.Err() != nil {
			return Identity{}, err
		}
		return Identity{}, ErrBadCredentials
	}
	if err != nil {
		return Identity{}, err
	}
	ok, err := s.hasher.Verify(ctx, password, hash)
	switch {
	case err != nil:
		return Identity{}, err
	case !ok:
		return Identity{}, ErrBadCredentials
	case user.Disabled:
		return Identity{}, ErrBadCredentials // the same answer as a wrong password
	}
	return Identity{User: &user, cred: credential{kind: byPassword, secret: hash}}, nil
}

func (s *Service) byToken(ctx context.Context, secret string) (Identity, error) {
	if !LooksLikeToken(secret) {
		return Identity{}, ErrBadCredentials
	}
	hash := HashToken(secret)
	token, user, err := s.acc.TokenByHash(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return Identity{}, ErrBadCredentials
	}
	if err != nil {
		return Identity{}, err
	}
	if token.ExpiresAt != nil && !token.ExpiresAt.After(s.cfg.Now()) {
		return Identity{}, ErrBadCredentials
	}
	if user.Disabled {
		return Identity{}, ErrBadCredentials
	}
	s.touch(ctx, token.ID)
	return Identity{User: &user, cred: credential{kind: byToken, secret: hash}}, nil
}

// touch records that a token was used, at most once per touchInterval.
func (s *Service) touch(ctx context.Context, tokenID string) {
	now := s.cfg.Now()
	s.mu.Lock()
	if last, ok := s.touched[tokenID]; ok && now.Sub(last) < touchInterval {
		s.mu.Unlock()
		return
	}
	if len(s.touched) > 10_000 {
		clear(s.touched) // bounded: the worst case is a few extra writes
	}
	s.touched[tokenID] = now
	s.mu.Unlock()
	_ = s.acc.TouchToken(ctx, tokenID, now) // not worth failing a request over
}

// Revalidate implements Authorizer.
func (s *Service) Revalidate(ctx context.Context, id Identity) (Identity, error) {
	switch id.cred.kind {
	case byPassword:
		user, hash, err := s.acc.UserByID(ctx, id.User.ID)
		if errors.Is(err, ErrNotFound) || (err == nil && (hash != id.cred.secret || user.Disabled)) {
			return Identity{}, ErrBadCredentials // deleted, disabled, or the password was changed
		}
		if err != nil {
			return Identity{}, err
		}
		return Identity{User: &user, cred: id.cred}, nil
	case byToken:
		token, user, err := s.acc.TokenByHash(ctx, id.cred.secret)
		if errors.Is(err, ErrNotFound) {
			return Identity{}, ErrBadCredentials // revoked, or the user was deleted
		}
		if err != nil {
			return Identity{}, err
		}
		if token.ExpiresAt != nil && !token.ExpiresAt.After(s.cfg.Now()) {
			return Identity{}, ErrBadCredentials
		}
		if user.Disabled {
			return Identity{}, ErrBadCredentials
		}
		return Identity{User: &user, cred: id.cred}, nil
	}
	return id, nil // anonymous: nothing to go stale
}

// --- authorizing -------------------------------------------------------------

// Authorize implements Authorizer.
func (s *Service) Authorize(ctx context.Context, id Identity, topic string, p Permission) error {
	if id.User != nil && id.User.Role == RoleAdmin {
		return nil
	}
	rules, err := s.acc.RulesFor(ctx, id.UserID())
	if err != nil {
		return err
	}
	if Decide(rules, id.UserID(), topic, s.Default()).Allows(p) {
		return nil
	}
	if id.Anonymous() {
		return fmt.Errorf("%w: log in to %s this topic", ErrUnauthorized, p)
	}
	return fmt.Errorf("%w: you may not %s this topic", ErrForbidden, p)
}

// MayAs says whether the user with the given ID, or an anonymous visitor for "",
// may do p to topic. It is Authorize for code that acts for someone who isn't
// making a request, such as a push notification going to their phone. A user who
// no longer exists may do nothing.
func (s *Service) MayAs(ctx context.Context, userID, topic string, p Permission) (bool, error) {
	if userID != "" {
		user, _, err := s.acc.UserByID(ctx, userID)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if user.Disabled {
			return false, nil // a disabled user is woken for nothing
		}
		if user.Role == RoleAdmin {
			return true, nil
		}
	}
	rules, err := s.acc.RulesFor(ctx, userID)
	if err != nil {
		return false, err
	}
	return Decide(rules, userID, topic, s.Default()).Allows(p), nil
}

// MayRead is MayAs for reading, which is what a device's owner needs to be able
// to do for it to be woken for a topic.
func (s *Service) MayRead(ctx context.Context, userID, topic string) (bool, error) {
	return s.MayAs(ctx, userID, topic, Read)
}

// Rules returns the access rules that apply to id: its own and everyone's.
func (s *Service) Rules(ctx context.Context, id Identity) ([]Rule, error) {
	return s.acc.RulesFor(ctx, id.UserID())
}

// --- managing accounts -------------------------------------------------------

// CreateUser adds a user, checking the name and password against the rules.
func (s *Service) CreateUser(ctx context.Context, name, password string, role Role) (User, error) {
	if err := ValidUsername(name); err != nil {
		return User{}, &InputError{err.Error()}
	}
	if err := ValidPassword(password); err != nil {
		return User{}, &InputError{err.Error()}
	}
	if _, err := ParseRole(string(role)); err != nil {
		return User{}, &InputError{err.Error()}
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return User{}, err
	}
	u := User{ID: newID(), Name: name, Role: role, CreatedAt: s.cfg.Now()}
	if err := s.acc.CreateUser(ctx, u, hash); err != nil {
		if errors.Is(err, ErrExists) {
			return User{}, fmt.Errorf("a user named %q already exists: %w", name, ErrExists)
		}
		return User{}, err
	}
	return u, nil
}

// user finds a user by name.
func (s *Service) user(ctx context.Context, name string) (User, error) {
	u, _, err := s.acc.UserByName(ctx, strings.ToLower(name))
	if errors.Is(err, ErrNotFound) {
		return User{}, fmt.Errorf("there is no user named %q: %w", name, ErrNotFound)
	}
	return u, err
}

// SetPassword changes a user's password. Anything logged in with the old password
// is cut off the next time it is checked; tokens are not affected.
func (s *Service) SetPassword(ctx context.Context, name, password string) error {
	u, err := s.user(ctx, name)
	if err != nil {
		return err
	}
	if err := ValidPassword(password); err != nil {
		return &InputError{err.Error()}
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return err
	}
	return s.acc.SetPassword(ctx, u.ID, hash)
}

// SetRole makes a user an administrator or an ordinary user. Open connections notice within
// the interval at which they re-check.
func (s *Service) SetRole(ctx context.Context, name string, role Role) error {
	if _, err := ParseRole(string(role)); err != nil {
		return &InputError{err.Error()}
	}
	u, err := s.user(ctx, name)
	if err != nil {
		return err
	}
	return s.acc.SetRole(ctx, u.ID, role)
}

// SetDisabled disables a user, or enables them again. A disabled user cannot log in, their
// tokens stop working and their open connections end within the interval at which they
// re-check; nothing of theirs is deleted.
func (s *Service) SetDisabled(ctx context.Context, name string, disabled bool) error {
	u, err := s.user(ctx, name)
	if err != nil {
		return err
	}
	return s.acc.SetDisabled(ctx, u.ID, disabled)
}

// DeleteUser removes a user, with their tokens and access rules.
func (s *Service) DeleteUser(ctx context.Context, name string) error {
	u, err := s.user(ctx, name)
	if err != nil {
		return err
	}
	return s.acc.DeleteUser(ctx, u.ID)
}

// Users lists every user.
func (s *Service) Users(ctx context.Context) ([]User, error) { return s.acc.ListUsers(ctx) }

// FindUser returns the user with the given name.
func (s *Service) FindUser(ctx context.Context, name string) (User, error) { return s.user(ctx, name) }

// MaxLabelLength is the longest label a token can have.
const MaxLabelLength = 64

// IssueToken creates an access token for a user and returns its secret, which is
// not kept and can't be shown again. A zero expiresIn means it never expires.
func (s *Service) IssueToken(ctx context.Context, userID, label string, expiresIn time.Duration) (string, Token, error) {
	if utf8.RuneCountInString(label) > MaxLabelLength || !utf8.ValidString(label) || strings.ContainsFunc(label, func(r rune) bool { return r < 0x20 || r == 0x7F }) {
		return "", Token{}, &InputError{fmt.Sprintf("a token label is up to %d characters on one line", MaxLabelLength)}
	}
	if expiresIn < 0 {
		return "", Token{}, &InputError{"a token can't expire in the past"}
	}
	existing, err := s.acc.ListTokens(ctx, userID)
	if err != nil {
		return "", Token{}, err
	}
	if len(existing) >= s.cfg.MaxTokensPerUser {
		return "", Token{}, fmt.Errorf("%w: a user can have at most %d; delete some first", ErrTooManyTokens, s.cfg.MaxTokensPerUser)
	}

	secret := NewToken()
	now := s.cfg.Now()
	t := Token{ID: newID(), UserID: userID, Label: label, CreatedAt: now}
	if expiresIn > 0 {
		exp := now.Add(expiresIn)
		t.ExpiresAt = &exp
	}
	if err := s.acc.CreateToken(ctx, t, HashToken(secret)); err != nil {
		return "", Token{}, err
	}
	return secret, t, nil
}

// Tokens lists a user's tokens (without their secrets).
func (s *Service) Tokens(ctx context.Context, userID string) ([]Token, error) {
	return s.acc.ListTokens(ctx, userID)
}

// DeleteToken revokes a token. With an owner given, only that user's tokens can be
// revoked; with "" any can.
func (s *Service) DeleteToken(ctx context.Context, tokenID, ownerID string) error {
	return s.acc.DeleteToken(ctx, tokenID, ownerID)
}

// Grant gives a user, or everyone, access to the topics a pattern matches. who is
// a user name, or Everyone.
func (s *Service) Grant(ctx context.Context, who, pattern string, access Access) error {
	if !ValidPattern(pattern) {
		return &InputError{fmt.Sprintf("%q is not a valid pattern: use a topic name, with * for any characters", pattern)}
	}
	if _, err := ParseAccess(string(access)); err != nil {
		return &InputError{fmt.Sprintf("access %v", err)}
	}
	userID, err := s.ruleOwner(ctx, who)
	if err != nil {
		return err
	}
	return s.acc.GrantAccess(ctx, Rule{UserID: userID, Pattern: pattern, Access: access})
}

// Revoke removes the rule for a pattern from a user, or from everyone.
func (s *Service) Revoke(ctx context.Context, who, pattern string) error {
	userID, err := s.ruleOwner(ctx, who)
	if err != nil {
		return err
	}
	err = s.acc.RevokeAccess(ctx, userID, pattern)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%s has no rule for %q: %w", who, pattern, ErrNotFound)
	}
	return err
}

func (s *Service) ruleOwner(ctx context.Context, who string) (string, error) {
	if who == Everyone {
		return "", nil
	}
	u, err := s.user(ctx, who)
	return u.ID, err
}

// AllRules lists every rule, with the name of the user it is for ("everyone" for
// the ones that apply to all).
func (s *Service) AllRules(ctx context.Context) ([]NamedRule, error) {
	rules, err := s.acc.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.acc.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{"": Everyone}
	for _, u := range users {
		names[u.ID] = u.Name
	}
	out := make([]NamedRule, len(rules))
	for i, r := range rules {
		out[i] = NamedRule{Who: names[r.UserID], Pattern: r.Pattern, Access: r.Access}
	}
	return out, nil
}

// NamedRule is a Rule with the owner's name instead of ID.
type NamedRule struct {
	Who     string // a user name, or Everyone
	Pattern string
	Access  Access
}
