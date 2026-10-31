package auth_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/store"
)

var ctx = context.Background()

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// setup returns a service over a real SQLite database, with cheap password
// hashing and a clock the test controls.
func setup(t *testing.T, edit ...func(*auth.ServiceConfig)) (*auth.Service, *clock, *store.SQLite) {
	t.Helper()
	st, err := store.OpenSQLite(ctx, filepath.Join(t.TempDir(), "flare.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	cfg := auth.ServiceConfig{
		Default:    auth.DenyAll,
		HashParams: auth.Params{Time: 1, MemoryKiB: 64, Threads: 1},
		Now:        clk.Now,
	}
	for _, e := range edit {
		e(&cfg)
	}
	return auth.NewService(st, cfg), clk, st
}

const pw = "correct horse battery"

func addUser(t *testing.T, s *auth.Service, name string, role auth.Role) auth.User {
	t.Helper()
	u, err := s.CreateUser(ctx, name, pw, role)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func basic(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

func request(authorization, remote string) *http.Request {
	r := httptest.NewRequest("GET", "/t", nil)
	r.RemoteAddr = remote
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	return r
}

func authenticate(s *auth.Service, authorization string) (auth.Identity, error) {
	return s.Authenticate(request(authorization, "203.0.113.7:4000"))
}

func TestNoCredentialsMeansAnonymous(t *testing.T) {
	s, _, _ := setup(t)
	id, err := authenticate(s, "")
	if err != nil || !id.Anonymous() {
		t.Errorf("got %+v, %v; want an anonymous identity", id, err)
	}
}

func TestLoggingInWithAPassword(t *testing.T) {
	s, _, _ := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)

	id, err := authenticate(s, basic("alice", pw))
	if err != nil || id.Anonymous() || id.User.ID != alice.ID || id.User.Role != auth.RoleUser {
		t.Fatalf("got %+v, %v", id, err)
	}
	if id, err = authenticate(s, basic("ALICE", pw)); err != nil || id.Anonymous() {
		t.Errorf("the user name should not be case-sensitive: %v", err)
	}
	if id, err = authenticate(s, "basic "+strings.TrimPrefix(basic("alice", pw), "Basic ")); err != nil || id.Anonymous() {
		t.Errorf("the scheme should not be case-sensitive: %v", err)
	}
}

func TestAWrongPasswordAndAnUnknownUserAreIndistinguishable(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	_, wrong := authenticate(s, basic("alice", "not the password"))
	_, unknown := authenticate(s, basic("nobody", pw))
	if !errors.Is(wrong, auth.ErrBadCredentials) || !errors.Is(unknown, auth.ErrBadCredentials) {
		t.Fatalf("got %v and %v, want ErrBadCredentials for both", wrong, unknown)
	}
	if wrong.Error() != unknown.Error() {
		t.Errorf("%q vs %q: the error must not reveal whether the user exists", wrong, unknown)
	}
}

func TestMalformedCredentialsAreRefusedNotTreatedAsAnonymous(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	valid := auth.NewToken()
	n := 0
	for name, header := range map[string]string{
		"basic with nothing":           "Basic",
		"basic with spaces only":       "Basic   ",
		"basic that is not base64":     "Basic !!!!",
		"basic without a colon":        "Basic " + base64.StdEncoding.EncodeToString([]byte("alice")),
		"basic without a user name":    basic("", pw),
		"basic with an empty password": basic("alice", ""),
		"basic with a long user name":  basic(strings.Repeat("a", 65), pw),
		"basic with a huge password":   basic("alice", strings.Repeat("x", 100_000)),
		"an unsupported scheme":        "Digest username=alice",
		"just a word":                  "alice",
		"bearer with nothing":          "Bearer",
		"bearer with garbage":          "Bearer not-a-token",
		"bearer with an unknown token": "Bearer " + valid,
		"a password given as a bearer": "Bearer " + pw,
	} {
		// Each from its own address, so that the throttle (tested below) isn't what answers.
		n++
		id, err := s.Authenticate(request(header, fmt.Sprintf("203.0.113.%d:1", n)))
		if !errors.Is(err, auth.ErrBadCredentials) || !id.Anonymous() {
			t.Errorf("%s: got %+v, %v; want ErrBadCredentials", name, id, err)
		}
	}
}

func TestAccessTokens(t *testing.T) {
	s, clk, st := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)

	secret, tok, err := s.IssueToken(ctx, alice.ID, "phone", 0)
	if err != nil || !auth.LooksLikeToken(secret) || tok.Label != "phone" || tok.ExpiresAt != nil {
		t.Fatalf("IssueToken = %q, %+v, %v", secret, tok, err)
	}
	id, err := authenticate(s, "Bearer "+secret)
	if err != nil || id.Anonymous() || id.User.Name != "alice" {
		t.Fatalf("got %+v, %v", id, err)
	}
	if id, err = authenticate(s, "bearer "+secret); err != nil || id.Anonymous() {
		t.Errorf("the scheme should not be case-sensitive: %v", err)
	}

	// Only the hash is stored.
	if _, _, err := st.TokenByHash(ctx, secret); !errors.Is(err, auth.ErrNotFound) {
		t.Error("the token itself is stored, not just its hash")
	}
	if _, _, err := st.TokenByHash(ctx, auth.HashToken(secret)); err != nil {
		t.Errorf("the token's hash is not stored: %v", err)
	}

	// Revoking it ends it.
	if err := s.DeleteToken(ctx, tok.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(s, "Bearer "+secret); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("a revoked token still works: %v", err)
	}
	_ = clk
}

func TestATokenExpires(t *testing.T) {
	s, clk, _ := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)
	secret, tok, _ := s.IssueToken(ctx, alice.ID, "", time.Hour)
	if tok.ExpiresAt == nil || !tok.ExpiresAt.Equal(clk.Now().Add(time.Hour)) {
		t.Fatalf("expires %v", tok.ExpiresAt)
	}
	clk.Advance(59 * time.Minute)
	if _, err := authenticate(s, "Bearer "+secret); err != nil {
		t.Errorf("before it expires: %v", err)
	}
	clk.Advance(2 * time.Minute)
	if _, err := authenticate(s, "Bearer "+secret); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("after it expires: %v", err)
	}
}

func TestDeletingAUserEndsTheirTokens(t *testing.T) {
	s, _, _ := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)
	secret, _, _ := s.IssueToken(ctx, alice.ID, "", 0)
	if err := s.DeleteUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(s, "Bearer "+secret); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("a deleted user's token still works: %v", err)
	}
	if _, err := authenticate(s, basic("alice", pw)); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("a deleted user's password still works: %v", err)
	}
}

func TestTokenUseIsRecordedAtMostOncePerMinute(t *testing.T) {
	s, clk, st := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)
	secret, tok, _ := s.IssueToken(ctx, alice.ID, "", 0)
	lastUsed := func() *time.Time {
		list, _ := st.ListTokens(ctx, alice.ID)
		return list[0].LastUsedAt
	}
	if lastUsed() != nil {
		t.Fatal("a new token claims to have been used")
	}
	authenticate(s, "Bearer "+secret)
	first := lastUsed()
	if first == nil || !first.Equal(clk.Now()) {
		t.Fatalf("last used = %v, want now", first)
	}
	clk.Advance(30 * time.Second)
	authenticate(s, "Bearer "+secret)
	if got := lastUsed(); !got.Equal(*first) {
		t.Errorf("last used moved to %v after 30s: it should be written at most once a minute", got)
	}
	clk.Advance(31 * time.Second)
	authenticate(s, "Bearer "+secret)
	if got := lastUsed(); got.Equal(*first) {
		t.Errorf("last used still %v after a minute", got)
	}
	_ = tok
}

func TestTokenLimitPerUser(t *testing.T) {
	s, _, _ := setup(t, func(c *auth.ServiceConfig) { c.MaxTokensPerUser = 3 })
	alice, bob := addUser(t, s, "alice", auth.RoleUser), addUser(t, s, "bob", auth.RoleUser)
	for range 3 {
		if _, _, err := s.IssueToken(ctx, alice.ID, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.IssueToken(ctx, alice.ID, "", 0); !errors.Is(err, auth.ErrTooManyTokens) {
		t.Errorf("the fourth token: %v, want ErrTooManyTokens", err)
	}
	if _, _, err := s.IssueToken(ctx, bob.ID, "", 0); err != nil {
		t.Errorf("another user's tokens must not count: %v", err)
	}
}

func TestTokenLabelsAndExpiryAreChecked(t *testing.T) {
	s, _, _ := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)
	for _, label := range []string{strings.Repeat("l", 65), "two\nlines", "bell\x07", "\xff\xfe"} {
		if _, _, err := s.IssueToken(ctx, alice.ID, label, 0); err == nil {
			t.Errorf("label %q was accepted", label)
		}
	}
	if _, _, err := s.IssueToken(ctx, alice.ID, "ünïcode ✓", 0); err != nil {
		t.Errorf("a label with unicode was refused: %v", err)
	}
	if _, _, err := s.IssueToken(ctx, alice.ID, "x", -time.Hour); err == nil {
		t.Error("a token that expired in the past was issued")
	}
}

// --- throttling --------------------------------------------------------------

func TestTooManyFailuresFromOneAddressAreRefusedEvenWithTheRightPassword(t *testing.T) {
	s, clk, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	attacker := "198.51.100.9:1111"

	for range 10 {
		if _, err := s.Authenticate(request(basic("alice", "guess"), attacker)); !errors.Is(err, auth.ErrBadCredentials) {
			t.Fatalf("got %v", err)
		}
	}
	_, err := s.Authenticate(request(basic("alice", pw), attacker)) // the right one, too late
	var throttled *auth.ThrottledError
	if !errors.Is(err, auth.ErrThrottled) || !errors.As(err, &throttled) || throttled.RetryAfter <= 0 || throttled.RetryAfter > 10*time.Minute {
		t.Fatalf("got %v, want a ThrottledError that says when to try again", err)
	}
	if _, err := s.Authenticate(request("Bearer "+auth.NewToken(), attacker)); !errors.Is(err, auth.ErrThrottled) {
		t.Errorf("a token attempt from a throttled address: %v", err)
	}
	// Anonymous requests aren't login attempts; they are never throttled.
	if id, err := s.Authenticate(request("", attacker)); err != nil || !id.Anonymous() {
		t.Errorf("an anonymous request from a throttled address: %v", err)
	}
	// Somebody else is not affected.
	if _, err := s.Authenticate(request(basic("alice", pw), "203.0.113.50:1")); err != nil {
		t.Errorf("another address was affected: %v", err)
	}
	clk.Advance(11 * time.Minute)
	if _, err := s.Authenticate(request(basic("alice", pw), attacker)); err != nil {
		t.Errorf("after the window: %v", err)
	}
}

func TestSuccessfulLoginsAreNeverCounted(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	for range 50 {
		if _, err := authenticate(s, basic("alice", pw)); err != nil {
			t.Fatalf("a legitimate client was throttled: %v", err)
		}
	}
}

func TestFailedTokensCountToo(t *testing.T) {
	s, _, _ := setup(t)
	for range 10 {
		s.Authenticate(request("Bearer "+auth.NewToken(), "198.51.100.9:1"))
	}
	if _, err := s.Authenticate(request("Bearer "+auth.NewToken(), "198.51.100.9:1")); !errors.Is(err, auth.ErrThrottled) {
		t.Errorf("guessing tokens wasn't throttled: %v", err)
	}
}

func TestSpoofingForwardedForDoesNotEvadeTheThrottle(t *testing.T) {
	s, _, _ := setup(t) // no trusted proxies: the header means nothing
	for i := range 10 {
		r := request(basic("alice", "guess"), "198.51.100.9:1")
		r.Header.Set("X-Forwarded-For", "10.0.0."+string(rune('1'+i)))
		s.Authenticate(r)
	}
	r := request(basic("alice", "guess"), "198.51.100.9:1")
	r.Header.Set("X-Forwarded-For", "10.9.9.9")
	if _, err := s.Authenticate(r); !errors.Is(err, auth.ErrThrottled) {
		t.Errorf("a new X-Forwarded-For got the attacker a fresh allowance: %v", err)
	}
}

func TestBehindATrustedProxyClientsAreCountedSeparately(t *testing.T) {
	proxies, _ := auth.ParseProxies([]string{"10.0.0.0/8"})
	s, _, _ := setup(t, func(c *auth.ServiceConfig) { c.Proxies = proxies })
	addUser(t, s, "alice", auth.RoleUser)
	viaProxy := func(client, authorization string) error {
		r := request(authorization, "10.0.0.2:5555")
		r.Header.Set("X-Forwarded-For", client)
		_, err := s.Authenticate(r)
		return err
	}

	for range 10 {
		viaProxy("203.0.113.66", basic("alice", "guess"))
	}
	if err := viaProxy("203.0.113.66", basic("alice", pw)); !errors.Is(err, auth.ErrThrottled) {
		t.Errorf("the attacker: %v", err)
	}
	if err := viaProxy("203.0.113.77", basic("alice", pw)); err != nil {
		t.Errorf("a different client behind the same proxy was throttled with the attacker: %v", err)
	}
	// Prepending made-up addresses doesn't help the attacker.
	if err := viaProxy("1.2.3.4, 203.0.113.66", basic("alice", pw)); !errors.Is(err, auth.ErrThrottled) {
		t.Errorf("the attacker, hiding behind a made-up address: %v", err)
	}
}

func TestLoginNeedsAPasswordNotAToken(t *testing.T) {
	s, _, _ := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)
	secret, _, _ := s.IssueToken(ctx, alice.ID, "", 0)

	if _, err := s.Login(request("Bearer "+secret, "203.0.113.7:1")); !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("logging in with a token: %v, want ErrUnauthorized", err)
	}
	if _, err := s.Login(request("", "203.0.113.7:1")); !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("logging in with nothing: %v, want ErrUnauthorized", err)
	}
	if id, err := s.Login(request(basic("alice", pw), "203.0.113.7:1")); err != nil || id.User.Name != "alice" {
		t.Errorf("logging in with the password: %+v, %v", id, err)
	}
	if _, err := s.Login(request(basic("alice", "wrong"), "203.0.113.7:1")); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("a wrong password: %v", err)
	}
}

func TestAuthenticateTokenForOtherTransports(t *testing.T) {
	s, _, _ := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)
	secret, _, _ := s.IssueToken(ctx, alice.ID, "", 0)
	if id, err := s.AuthenticateToken(ctx, secret, "203.0.113.7"); err != nil || id.User.Name != "alice" {
		t.Errorf("got %+v, %v", id, err)
	}
	for range 10 {
		s.AuthenticateToken(ctx, "junk", "198.51.100.9")
	}
	if _, err := s.AuthenticateToken(ctx, secret, "198.51.100.9"); !errors.Is(err, auth.ErrThrottled) {
		t.Errorf("failures over this route should be throttled too: %v", err)
	}
}

// --- authorizing -------------------------------------------------------------

func TestAuthorizeUsesTheDefaultWhereNoRuleApplies(t *testing.T) {
	s, _, _ := setup(t, func(c *auth.ServiceConfig) { c.Default = auth.ReadOnly })
	alice := addUser(t, s, "alice", auth.RoleUser)
	id, _ := authenticate(s, basic("alice", pw))
	var anon auth.Identity

	if err := s.Authorize(ctx, anon, "t", auth.Read); err != nil {
		t.Errorf("anonymous read on a read-only server: %v", err)
	}
	if err := s.Authorize(ctx, id, "t", auth.Read); err != nil {
		t.Errorf("user read: %v", err)
	}
	// Refused: an anonymous visitor is told to log in; a user is told no.
	if err := s.Authorize(ctx, anon, "t", auth.Write); !errors.Is(err, auth.ErrUnauthorized) || errors.Is(err, auth.ErrForbidden) {
		t.Errorf("anonymous write: %v, want ErrUnauthorized", err)
	}
	err := s.Authorize(ctx, id, "t", auth.Write)
	if !errors.Is(err, auth.ErrForbidden) || errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("user write: %v, want ErrForbidden", err)
	}
	_ = alice
}

func TestAuthorizeAppliesRules(t *testing.T) {
	s, _, _ := setup(t) // deny-all by default
	addUser(t, s, "alice", auth.RoleUser)
	addUser(t, s, "bob", auth.RoleUser)
	for _, g := range []struct {
		who, pattern string
		access       auth.Access
	}{
		{"alice", "alerts-*", auth.ReadWrite},
		{"alice", "alerts-secret", auth.DenyAll},
		{"bob", "alerts-*", auth.ReadOnly},
		{auth.Everyone, "status", auth.ReadOnly},
	} {
		if err := s.Grant(ctx, g.who, g.pattern, g.access); err != nil {
			t.Fatal(err)
		}
	}
	alice, _ := authenticate(s, basic("alice", pw))
	bob, _ := authenticate(s, basic("bob", pw))
	var anon auth.Identity

	check := func(who string, id auth.Identity, topic string, p auth.Permission, wantAllowed bool) {
		t.Helper()
		if err := s.Authorize(ctx, id, topic, p); (err == nil) != wantAllowed {
			t.Errorf("%s %s %q: err = %v, want allowed=%v", who, p, topic, err, wantAllowed)
		}
	}
	check("alice", alice, "alerts-db", auth.Write, true)
	check("alice", alice, "alerts-db", auth.Read, true)
	check("alice", alice, "alerts-secret", auth.Read, false) // an exact deny beats the pattern
	check("alice", alice, "other", auth.Read, false)
	check("bob", bob, "alerts-db", auth.Read, true)
	check("bob", bob, "alerts-db", auth.Write, false)
	check("anonymous", anon, "alerts-db", auth.Read, false)
	for who, id := range map[string]auth.Identity{"alice": alice, "bob": bob, "anonymous": anon} {
		check(who, id, "status", auth.Read, true) // a rule for everyone
		check(who, id, "status", auth.Write, false)
	}
}

func TestAdministratorsMayDoEverything(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "root", auth.RoleAdmin)
	id, _ := authenticate(s, basic("root", pw))
	if id.User.Role != auth.RoleAdmin {
		t.Fatal("not an admin")
	}
	for _, p := range []auth.Permission{auth.Read, auth.Write} {
		if err := s.Authorize(ctx, id, "anything", p); err != nil {
			t.Errorf("admin %s: %v", p, err)
		}
	}
	// A rule can't take it away: administrators are not subject to the rules.
	s.Grant(ctx, "root", "locked", auth.DenyAll)
	if err := s.Authorize(ctx, id, "locked", auth.Write); err != nil {
		t.Errorf("a deny rule bound an administrator: %v", err)
	}
}

func TestChangedRulesTakeEffectAtOnce(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	id, _ := authenticate(s, basic("alice", pw))
	if s.Authorize(ctx, id, "t", auth.Read) == nil {
		t.Fatal("allowed before any rule")
	}
	s.Grant(ctx, "alice", "t", auth.ReadOnly)
	if err := s.Authorize(ctx, id, "t", auth.Read); err != nil {
		t.Errorf("after granting: %v", err)
	}
	s.Revoke(ctx, "alice", "t")
	if s.Authorize(ctx, id, "t", auth.Read) == nil {
		t.Error("still allowed after revoking")
	}
}

// --- revalidating ------------------------------------------------------------

func TestRevalidate(t *testing.T) {
	s, clk, _ := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)

	var anon auth.Identity
	if fresh, err := s.Revalidate(ctx, anon); err != nil || !fresh.Anonymous() {
		t.Errorf("anonymous: %v", err)
	}

	byPassword, _ := authenticate(s, basic("alice", pw))
	secret, tok, _ := s.IssueToken(ctx, alice.ID, "", time.Hour)
	byToken, _ := authenticate(s, "Bearer "+secret)
	for name, id := range map[string]auth.Identity{"password": byPassword, "token": byToken} {
		if fresh, err := s.Revalidate(ctx, id); err != nil || fresh.User.Name != "alice" {
			t.Errorf("%s while still good: %+v, %v", name, fresh, err)
		}
	}

	// Revoking the token ends that identity only.
	s.DeleteToken(ctx, tok.ID, alice.ID)
	if _, err := s.Revalidate(ctx, byToken); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("a revoked token: %v", err)
	}
	if _, err := s.Revalidate(ctx, byPassword); err != nil {
		t.Errorf("the password identity was affected by revoking a token: %v", err)
	}

	// Changing the password ends the identity that used the old one.
	if err := s.SetPassword(ctx, "alice", "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revalidate(ctx, byPassword); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("after the password changed: %v", err)
	}

	// An expired token, and a deleted user.
	secret2, _, _ := s.IssueToken(ctx, alice.ID, "", time.Hour)
	id2, _ := authenticate(s, "Bearer "+secret2)
	clk.Advance(2 * time.Hour)
	if _, err := s.Revalidate(ctx, id2); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("an expired token: %v", err)
	}
	secret3, _, _ := s.IssueToken(ctx, alice.ID, "", 0)
	id3, _ := authenticate(s, "Bearer "+secret3)
	s.DeleteUser(ctx, "alice")
	if _, err := s.Revalidate(ctx, id3); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("a deleted user: %v", err)
	}
}

// --- managing ----------------------------------------------------------------

func TestCreateUserChecksItsInput(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	for name, tt := range map[string]struct {
		user, password string
		role           auth.Role
		want           string
	}{
		"a bad name":       {"Alice", pw, auth.RoleUser, "lower-case"},
		"a reserved name":  {"everyone", pw, auth.RoleUser, "reserved"},
		"a short password": {"bob", "short", auth.RoleUser, "at least"},
		"a bad role":       {"bob", pw, "root", "role"},
		"a duplicate":      {"alice", pw, auth.RoleUser, "already exists"},
	} {
		_, err := s.CreateUser(ctx, tt.user, tt.password, tt.role)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tt.want)
		}
	}
	if _, err := s.CreateUser(ctx, "alice", pw, auth.RoleUser); !errors.Is(err, auth.ErrExists) {
		t.Errorf("a duplicate should wrap ErrExists: %v", err)
	}
}

func TestPasswordsAreNotStoredInTheClear(t *testing.T) {
	s, _, st := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	_, hash, _ := st.UserByName(ctx, "alice")
	if strings.Contains(hash, pw) || !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("stored value %q is not an Argon2id hash", hash)
	}
}

func TestSetPassword(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	if err := s.SetPassword(ctx, "alice", "another long password"); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(s, basic("alice", pw)); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("the old password still works: %v", err)
	}
	if _, err := authenticate(s, basic("alice", "another long password")); err != nil {
		t.Errorf("the new password: %v", err)
	}
	if err := s.SetPassword(ctx, "alice", "short"); err == nil {
		t.Error("a weak password was accepted")
	}
	if err := s.SetPassword(ctx, "nobody", "another long password"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("an unknown user: %v", err)
	}
}

func TestGrantAndRevokeCheckTheirInput(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	if err := s.Grant(ctx, "nobody", "t", auth.ReadWrite); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("an unknown user: %v", err)
	}
	if err := s.Grant(ctx, "alice", "a/b", auth.ReadWrite); err == nil {
		t.Error("an invalid pattern was accepted")
	}
	if err := s.Grant(ctx, "alice", "t", "superuser"); err == nil {
		t.Error("an invalid access level was accepted")
	}
	if err := s.Grant(ctx, auth.Everyone, "t", auth.ReadOnly); err != nil {
		t.Errorf("a rule for everyone: %v", err)
	}
	if err := s.Revoke(ctx, "alice", "t"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("revoking a rule that is not there: %v", err)
	}
	if err := s.Revoke(ctx, auth.Everyone, "t"); err != nil {
		t.Errorf("revoking the rule for everyone: %v", err)
	}
}

func TestAllRulesNamesTheirOwners(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	s.Grant(ctx, "alice", "a", auth.ReadWrite)
	s.Grant(ctx, auth.Everyone, "pub", auth.ReadOnly)
	rules, err := s.AllRules(ctx)
	if err != nil || len(rules) != 2 || rules[0].Who != auth.Everyone || rules[1].Who != "alice" {
		t.Errorf("AllRules = %+v, %v", rules, err)
	}
}

func TestConcurrentAuthentication(t *testing.T) {
	s, _, _ := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)
	secret, _, _ := s.IssueToken(ctx, alice.ID, "", 0)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 30 {
				var h string
				switch (g + i) % 3 {
				case 0:
					h = "Bearer " + secret
				case 1:
					h = basic("alice", pw)
				default:
					h = ""
				}
				id, err := s.Authenticate(request(h, "203.0.113.7:1"))
				if err != nil {
					t.Error(err)
					return
				}
				s.Authorize(ctx, id, "t", auth.Read)
				s.Revalidate(ctx, id)
			}
		}()
	}
	wg.Wait()
}

func TestMayAsActsForSomeoneWhoIsNotMakingARequest(t *testing.T) {
	s, _, _ := setup(t) // deny-all by default
	alice := addUser(t, s, "alice", auth.RoleUser)
	admin := addUser(t, s, "root", auth.RoleAdmin)
	s.Grant(ctx, "alice", "alerts-*", auth.ReadOnly)
	s.Grant(ctx, "alice", "secret", auth.DenyAll)
	s.Grant(ctx, "everyone", "status", auth.ReadOnly)

	tests := []struct {
		who   string
		topic string
		p     auth.Permission
		want  bool
	}{
		{alice.ID, "alerts-db", auth.Read, true},
		{alice.ID, "alerts-db", auth.Write, false},
		{alice.ID, "other", auth.Read, false},
		{alice.ID, "secret", auth.Read, false},
		{alice.ID, "status", auth.Read, true}, // a rule for everyone applies to users too
		{"", "status", auth.Read, true},       // and to an anonymous visitor
		{"", "alerts-db", auth.Read, false},   // but alice's rules do not
		{"", "other", auth.Read, false},
		{admin.ID, "anything", auth.Read, true},
		{admin.ID, "anything", auth.Write, true},
		{"no-such-user", "status", auth.Read, false}, // a deleted user may do nothing, even what everyone may
	}
	for _, tt := range tests {
		got, err := s.MayAs(ctx, tt.who, tt.topic, tt.p)
		if err != nil || got != tt.want {
			t.Errorf("MayAs(%q, %q, %v) = %v, %v; want %v", tt.who, tt.topic, tt.p, got, err, tt.want)
		}
	}

	// It sees changes at once: nothing is cached.
	s.Revoke(ctx, "alice", "alerts-*")
	if ok, _ := s.MayAs(ctx, alice.ID, "alerts-db", auth.Read); ok {
		t.Error("a revoked rule still allows")
	}
	s.DeleteUser(ctx, "alice")
	if ok, _ := s.MayAs(ctx, alice.ID, "status", auth.Read); ok {
		t.Error("a deleted user still allowed")
	}
}

func TestWhatIsAllowedByDefaultCanChangeWhileRunning(t *testing.T) {
	s, _, _ := setup(t, func(c *auth.ServiceConfig) { c.Default = auth.ReadWrite })
	anon := auth.Identity{}
	if err := s.Authorize(ctx, anon, "alerts", auth.Write); err != nil {
		t.Fatalf("open server: %v", err)
	}

	s.SetDefault(auth.DenyAll)
	if s.Default() != auth.DenyAll {
		t.Errorf("Default = %v", s.Default())
	}
	if err := s.Authorize(ctx, anon, "alerts", auth.Write); err == nil {
		t.Error("an anonymous visitor may write on a closed server")
	}
	if ok, err := s.MayAs(ctx, "nobody", "alerts", auth.Read); err != nil || ok {
		t.Errorf("MayAs on a closed server: %v %v", ok, err)
	}

	s.SetDefault(auth.ReadOnly)
	if err := s.Authorize(ctx, anon, "alerts", auth.Read); err != nil {
		t.Errorf("read on a read-only server: %v", err)
	}
	if err := s.Authorize(ctx, anon, "alerts", auth.Write); err == nil {
		t.Error("write on a read-only server")
	}
}

func TestChangingTheDefaultWhileAuthorizingIsSafe(t *testing.T) {
	s, _, _ := setup(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.SetDefault([]auth.Access{auth.DenyAll, auth.ReadWrite}[j%2])
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = s.Authorize(ctx, auth.Identity{}, "alerts", auth.Read)
			}
		}()
	}
	wg.Wait()
}

// --- disabling users, and changing their role --------------------------------

func TestADisabledUserCannotLogInWithAPasswordAndIsToldNothingExtra(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	if err := s.SetDisabled(ctx, "alice", true); err != nil {
		t.Fatal(err)
	}
	_, err := authenticate(s, basic("alice", pw))
	if !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("a disabled user's right password: %v", err)
	}
	// The same answer as a wrong password, so it is not a way to find out who is disabled.
	_, wrong := authenticate(s, basic("alice", "not the password"))
	if err.Error() != wrong.Error() {
		t.Errorf("answers differ: %q and %q", err, wrong)
	}
}

func TestADisabledUsersTokensStopWorkingAndWorkAgainWhenTheyAreEnabled(t *testing.T) {
	s, _, _ := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)
	secret, _, err := s.IssueToken(ctx, alice.ID, "laptop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(s, "Bearer "+secret); err != nil {
		t.Fatalf("before: %v", err)
	}

	if err := s.SetDisabled(ctx, "alice", true); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(s, "Bearer "+secret); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("a disabled user's token: %v", err)
	}
	if tokens, _ := s.Tokens(ctx, alice.ID); len(tokens) != 1 {
		t.Errorf("a disabled user's tokens were deleted: %d", len(tokens))
	}

	if err := s.SetDisabled(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(s, "Bearer "+secret); err != nil {
		t.Errorf("after enabling: %v", err)
	}
	if _, err := authenticate(s, basic("alice", pw)); err != nil {
		t.Errorf("after enabling, with the password: %v", err)
	}
}

func TestWhatADisabledUserHasOpenIsCutOffAtTheNextCheck(t *testing.T) {
	s, _, _ := setup(t)
	alice := addUser(t, s, "alice", auth.RoleUser)
	secret, _, _ := s.IssueToken(ctx, alice.ID, "", 0)
	byToken, err := authenticate(s, "Bearer "+secret)
	if err != nil {
		t.Fatal(err)
	}
	byPassword, err := authenticate(s, basic("alice", pw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revalidate(ctx, byToken); err != nil {
		t.Fatalf("still enabled: %v", err)
	}

	if err := s.SetDisabled(ctx, "alice", true); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]auth.Identity{"token": byToken, "password": byPassword} {
		if _, err := s.Revalidate(ctx, id); !errors.Is(err, auth.ErrBadCredentials) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestADisabledUserIsWokenForNothing(t *testing.T) {
	s, _, _ := setup(t, func(c *auth.ServiceConfig) { c.Default = auth.ReadWrite })
	alice := addUser(t, s, "alice", auth.RoleUser)
	root := addUser(t, s, "root", auth.RoleAdmin)
	for _, u := range []auth.User{alice, root} {
		if ok, _ := s.MayAs(ctx, u.ID, "alerts", auth.Read); !ok {
			t.Fatalf("%s before: not allowed", u.Name)
		}
		if err := s.SetDisabled(ctx, u.Name, true); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.MayAs(ctx, u.ID, "alerts", auth.Read); err != nil || ok {
			t.Errorf("%s disabled: %v %v", u.Name, ok, err)
		}
	}
}

func TestDisablingAndRoleChangesNeedAnExistingUser(t *testing.T) {
	s, _, _ := setup(t)
	if err := s.SetDisabled(ctx, "nobody", true); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("disable: %v", err)
	}
	if err := s.SetRole(ctx, "nobody", auth.RoleAdmin); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("role: %v", err)
	}
}

func TestChangingARole(t *testing.T) {
	s, _, _ := setup(t, func(c *auth.ServiceConfig) { c.Default = auth.DenyAll })
	alice := addUser(t, s, "alice", auth.RoleUser)
	id, _ := authenticate(s, basic("alice", pw))
	if err := s.Authorize(ctx, id, "alerts", auth.Read); err == nil {
		t.Fatal("a user on a closed server may read")
	}

	if err := s.SetRole(ctx, "alice", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	id, _ = authenticate(s, basic("alice", pw))
	if id.User.Role != auth.RoleAdmin || s.Authorize(ctx, id, "alerts", auth.Write) != nil {
		t.Errorf("as an administrator: %+v", id.User)
	}
	if err := s.SetRole(ctx, "alice", auth.RoleUser); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.FindUser(ctx, "alice"); u.Role != auth.RoleUser || u.ID != alice.ID {
		t.Errorf("back as a user: %+v", u)
	}

	var input *auth.InputError
	if err := s.SetRole(ctx, "alice", auth.Role("root")); !errors.As(err, &input) {
		t.Errorf("a role that does not exist: %v", err)
	}
}

func TestBadInputIsReportedAsInput(t *testing.T) {
	s, _, _ := setup(t)
	addUser(t, s, "alice", auth.RoleUser)
	var input *auth.InputError
	for what, err := range map[string]error{
		"a bad name":     func() error { _, e := s.CreateUser(ctx, "Bad Name", pw, auth.RoleUser); return e }(),
		"a short pass":   func() error { _, e := s.CreateUser(ctx, "bob", "short", auth.RoleUser); return e }(),
		"a bad role":     func() error { _, e := s.CreateUser(ctx, "bob", pw, auth.Role("king")); return e }(),
		"a new password": s.SetPassword(ctx, "alice", "short"),
		"a bad pattern":  s.Grant(ctx, "alice", "bad pattern!", auth.ReadOnly),
		"a bad access":   s.Grant(ctx, "alice", "alerts", auth.Access("sometimes")),
	} {
		if !errors.As(err, &input) {
			t.Errorf("%s: %v is not input", what, err)
		}
	}
	if _, err := s.CreateUser(ctx, "alice", pw, auth.RoleUser); !errors.Is(err, auth.ErrExists) {
		t.Errorf("a name taken: %v", err)
	}
}
