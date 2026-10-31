package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/config"
)

var bg = context.Background()

const secret = "correct horse battery"

// admin runs a user/token/access command against the database at db.
func admin(t *testing.T, db, stdin string, args ...string) result {
	t.Helper()
	return run(t, bg, stdin, nil, append(args, "--database", db)...)
}

func newDB(t *testing.T) string { return "sqlite://" + filepath.Join(t.TempDir(), "flare.db") }

func mustSucceed(t *testing.T, r result) result {
	t.Helper()
	if r.err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", r.err, r.stderr)
	}
	return r
}

func TestUserCommands(t *testing.T) {
	db := newDB(t)
	r := mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "alice"))
	if !strings.Contains(r.stderr, "Created user alice") {
		t.Errorf("stderr = %q", r.stderr)
	}
	mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "--admin", "root"))

	r = mustSucceed(t, admin(t, db, "", "user", "list"))
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") || !strings.Contains(lines[1], "alice") || !strings.Contains(lines[1], "user") ||
		!strings.Contains(lines[2], "root") || !strings.Contains(lines[2], "admin") {
		t.Errorf("user list =\n%s", r.stdout)
	}

	r = mustSucceed(t, admin(t, db, "a new password\n", "user", "passwd", "alice"))
	if !strings.Contains(r.stderr, "Changed the password of alice") {
		t.Errorf("stderr = %q", r.stderr)
	}
	mustSucceed(t, admin(t, db, "", "user", "delete", "alice"))
	r = mustSucceed(t, admin(t, db, "", "user", "list"))
	if strings.Contains(r.stdout, "alice") || !strings.Contains(r.stdout, "root") {
		t.Errorf("after the delete:\n%s", r.stdout)
	}
}

func TestUserCommandsRefuseBadInput(t *testing.T) {
	db := newDB(t)
	mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "alice"))
	for name, tt := range map[string]struct {
		stdin   string
		args    []string
		mention string
	}{
		"a duplicate":                    {secret + "\n", []string{"user", "add", "alice"}, "already exists"},
		"a bad name":                     {secret + "\n", []string{"user", "add", "Alice B"}, "lower-case"},
		"a reserved name":                {secret + "\n", []string{"user", "add", "everyone"}, "reserved"},
		"a short password":               {"short\n", []string{"user", "add", "bob"}, "at least"},
		"no password":                    {"", []string{"user", "add", "bob"}, "no password given"},
		"an empty line":                  {"\n", []string{"user", "add", "bob"}, "no password given"},
		"a password on the command line": {"", []string{"user", "add", "bob", "--password", secret}, "unknown flag"},
		"passwd for nobody":              {secret + "\n", []string{"user", "passwd", "nobody"}, "no user named"},
		"delete for nobody":              {"", []string{"user", "delete", "nobody"}, "no user named"},
		"no name":                        {"", []string{"user", "add"}, "accepts 1 arg"},
	} {
		r := admin(t, db, tt.stdin, tt.args...)
		if r.err == nil || !strings.Contains(r.err.Error(), tt.mention) {
			t.Errorf("%s: err = %v, want it to mention %q", name, r.err, tt.mention)
		}
	}
	if r := mustSucceed(t, admin(t, db, "", "user", "list")); strings.Contains(r.stdout, "bob") {
		t.Error("a refused command created a user")
	}
}

func TestThePasswordIsNeverEchoed(t *testing.T) {
	db := newDB(t)
	r := mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "alice"))
	if strings.Contains(r.stdout+r.stderr, secret) {
		t.Errorf("the password appeared in the output: %q %q", r.stdout, r.stderr)
	}
	r = admin(t, db, "short\n", "user", "add", "bob")
	if strings.Contains(r.err.Error()+r.stdout+r.stderr, "short") && strings.Contains(r.err.Error(), "short\n") {
		t.Errorf("a rejected password was echoed back: %v", r.err)
	}
}

func TestTokenCommands(t *testing.T) {
	db := newDB(t)
	mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "alice"))

	r := mustSucceed(t, admin(t, db, "", "token", "add", "--label", "backup-server", "--expires", "30d", "alice"))
	token := strings.TrimSpace(r.stdout)
	if !auth.LooksLikeToken(token) {
		t.Fatalf("stdout = %q, want only the token (so that it can be captured)", r.stdout)
	}
	if !strings.Contains(r.stderr, "shown only once") || strings.Contains(r.stderr, token) {
		t.Errorf("stderr = %q: it should explain, and must not repeat the token", r.stderr)
	}

	r = mustSucceed(t, admin(t, db, "", "token", "list", "alice"))
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[1], "backup-server") {
		t.Fatalf("token list =\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, token) {
		t.Error("the list shows the token itself")
	}
	id := strings.Fields(lines[1])[0]
	if strings.Fields(lines[1])[len(strings.Fields(lines[1]))-1] != "-" {
		t.Errorf("last used should be '-' for an unused token: %q", lines[1])
	}

	mustSucceed(t, admin(t, db, "", "token", "delete", id))
	r = mustSucceed(t, admin(t, db, "", "token", "list", "alice"))
	if len(strings.Split(strings.TrimSpace(r.stdout), "\n")) != 1 {
		t.Errorf("the token is still listed:\n%s", r.stdout)
	}
	if r := admin(t, db, "", "token", "delete", id); r.err == nil || !strings.Contains(r.err.Error(), "no token") {
		t.Errorf("deleting twice: %v", r.err)
	}
}

func TestTokenCommandsRefuseBadInput(t *testing.T) {
	db := newDB(t)
	mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "alice"))
	for name, args := range map[string][]string{
		"an unknown user":     {"token", "add", "nobody"},
		"a lifetime of zero":  {"token", "add", "--expires", "0d", "alice"},
		"a lifetime nonsense": {"token", "add", "--expires", "soon", "alice"},
		"a negative lifetime": {"token", "add", "--expires", "-5h", "alice"},
		"a lifetime too long": {"token", "add", "--expires", "99999d", "alice"},
		"a label too long":    {"token", "add", "--label", strings.Repeat("x", 65), "alice"},
		"listing for nobody":  {"token", "list", "nobody"},
	} {
		if r := admin(t, db, "", args...); r.err == nil {
			t.Errorf("%s: succeeded", name)
		}
	}
}

func TestParseLifetime(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"": 0, "30d": 30 * 24 * time.Hour, "1d": 24 * time.Hour, "12h": 12 * time.Hour, "90m": 90 * time.Minute, "3650d": 3650 * 24 * time.Hour,
	} {
		if got, err := parseLifetime(in); err != nil || got != want {
			t.Errorf("parseLifetime(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"0d", "-1d", "3651d", "d", "1.5d", "soon", "0s", "-5h", "10"} {
		if _, err := parseLifetime(bad); err == nil {
			t.Errorf("parseLifetime(%q) succeeded", bad)
		}
	}
}

func TestAccessCommands(t *testing.T) {
	db := newDB(t)
	mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "alice"))
	mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "bob"))
	mustSucceed(t, admin(t, db, "", "access", "grant", "alice", "alerts-*", "read-write"))
	mustSucceed(t, admin(t, db, "", "access", "grant", "bob", "alerts-*", "read-only"))
	r := mustSucceed(t, admin(t, db, "", "access", "grant", "everyone", "status", "read-only"))
	if !strings.Contains(r.stderr, "everyone now has read-only") {
		t.Errorf("stderr = %q", r.stderr)
	}
	mustSucceed(t, admin(t, db, "", "access", "grant", "alice", "alerts-*", "write-only")) // replaces

	r = mustSucceed(t, admin(t, db, "", "access", "list"))
	if strings.Count(r.stdout, "alerts-*") != 2 || !strings.Contains(r.stdout, "write-only") || strings.Contains(r.stdout, "read-write") {
		t.Errorf("access list =\n%s", r.stdout)
	}
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if !strings.HasPrefix(lines[1], "everyone") {
		t.Errorf("the rules for everyone come first:\n%s", r.stdout)
	}
	r = mustSucceed(t, admin(t, db, "", "access", "list", "bob"))
	if strings.Count(r.stdout, "\n") != 2 || !strings.Contains(r.stdout, "bob") || strings.Contains(r.stdout, "alice") {
		t.Errorf("access list bob =\n%s", r.stdout)
	}

	mustSucceed(t, admin(t, db, "", "access", "revoke", "alice", "alerts-*"))
	mustSucceed(t, admin(t, db, "", "access", "revoke", "everyone", "status"))
	r = mustSucceed(t, admin(t, db, "", "access", "list"))
	if strings.Contains(r.stdout, "alice") || strings.Contains(r.stdout, "status") || !strings.Contains(r.stdout, "bob") {
		t.Errorf("after revoking:\n%s", r.stdout)
	}
}

func TestAccessCommandsRefuseBadInput(t *testing.T) {
	db := newDB(t)
	mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "alice"))
	for name, tt := range map[string]struct {
		args    []string
		mention string
	}{
		"an unknown user":     {[]string{"access", "grant", "nobody", "t", "read-write"}, "no user named"},
		"a bad pattern":       {[]string{"access", "grant", "alice", "a/b", "read-write"}, "not a valid pattern"},
		"a bad level":         {[]string{"access", "grant", "alice", "t", "superuser"}, "must be one of"},
		"a missing argument":  {[]string{"access", "grant", "alice", "t"}, "accepts 3 arg"},
		"revoking nothing":    {[]string{"access", "revoke", "alice", "t"}, "has no rule"},
		"revoking for nobody": {[]string{"access", "revoke", "nobody", "t"}, "no user named"},
	} {
		r := admin(t, db, "", tt.args...)
		if r.err == nil || !strings.Contains(r.err.Error(), tt.mention) {
			t.Errorf("%s: err = %v, want it to mention %q", name, r.err, tt.mention)
		}
	}
}

func TestAdminCommandsFindTheDatabaseTheSameWayTheServerDoes(t *testing.T) {
	dir := t.TempDir()
	viaFlag := "sqlite://" + filepath.Join(dir, "flag.db")
	viaEnv := "sqlite://" + filepath.Join(dir, "env.db")
	viaFile := "sqlite://" + filepath.Join(dir, "file.db")
	cfg := filepath.Join(dir, "server.yml")
	os.WriteFile(cfg, []byte("database: "+viaFile+"\n"), 0o600)

	has := func(database, user string) bool {
		r := run(t, bg, "", nil, "user", "list", "--database", database)
		return r.err == nil && strings.Contains(r.stdout, user)
	}
	// The configuration file.
	run(t, bg, secret+"\n", nil, "user", "add", "--config", cfg, "fileuser")
	// The environment beats the file.
	run(t, bg, secret+"\n", map[string]string{"FLARE_DATABASE": viaEnv}, "user", "add", "--config", cfg, "envuser")
	// A flag beats both.
	run(t, bg, secret+"\n", map[string]string{"FLARE_DATABASE": viaEnv}, "user", "add", "--config", cfg, "--database", viaFlag, "flaguser")

	if !has(viaFile, "fileuser") || !has(viaEnv, "envuser") || !has(viaFlag, "flaguser") {
		t.Error("each user should be in the database its source selected")
	}
	if has(viaFile, "envuser") || has(viaFile, "flaguser") || has(viaEnv, "flaguser") {
		t.Error("a user was created in the wrong database")
	}
	if r := run(t, bg, "", nil, "user", "list", "--database", "postgres://x/y"); r.err == nil || !strings.Contains(r.err.Error(), "not supported yet") {
		t.Errorf("err = %v", r.err)
	}
}

// --- the whole flow, with a running server -----------------------------------

func TestSettingUpAServerThatRequiresLogin(t *testing.T) {
	url, db := serverWithDatabase(t, func(c *config.Config) { c.Auth.DefaultAccess = "deny-all" })

	// Nothing works for an anonymous visitor.
	if r := run(t, bg, "", nil, "publish", "-s", url, "alerts-db", "x"); r.err == nil || !strings.Contains(r.err.Error(), "log in") {
		t.Fatalf("anonymous publish: %v", r.err)
	}

	// The administrator sets things up with the commands, while the server runs.
	mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "alice"))
	mustSucceed(t, admin(t, db, "", "access", "grant", "alice", "alerts-*", "read-write"))
	token := strings.TrimSpace(mustSucceed(t, admin(t, db, "", "token", "add", "--label", "laptop", "alice")).stdout)

	// With the token (as a flag, and as the environment variable).
	if r := mustSucceed(t, run(t, bg, "", nil, "publish", "-s", url, "--token", token, "alerts-db", "via flag")); !strings.Contains(r.stdout, `"message":"via flag"`) {
		t.Errorf("stdout = %q", r.stdout)
	}
	env := map[string]string{"FLARE_TOKEN": token, "FLARE_SERVER": url}
	mustSucceed(t, run(t, bg, "", env, "publish", "alerts-db", "via environment"))
	r := mustSucceed(t, run(t, bg, "", env, "subscribe", "--poll", "--since", "all", "alerts-db"))
	if strings.Count(r.stdout, "\n") != 2 {
		t.Errorf("poll with the token = %q, want both messages", r.stdout)
	}

	// The token carries Alice's rules, no more.
	if r := run(t, bg, "", env, "publish", "other-topic", "x"); r.err == nil || !strings.Contains(r.err.Error(), "may not write") {
		t.Errorf("a topic with no rule: %v", r.err)
	}
	// A flag beats the environment, and a wrong token is refused loudly.
	if r := run(t, bg, "", env, "publish", "--token", "flr_"+strings.Repeat("x", 43), "alerts-db", "x"); r.err == nil || !strings.Contains(r.err.Error(), "invalid credentials") {
		t.Errorf("a wrong token: %v", r.err)
	}

	// Revoking the token cuts it off.
	id := strings.Fields(strings.Split(mustSucceed(t, admin(t, db, "", "token", "list", "alice")).stdout, "\n")[1])[0]
	mustSucceed(t, admin(t, db, "", "token", "delete", id))
	if r := run(t, bg, "", env, "publish", "alerts-db", "x"); r.err == nil || !strings.Contains(r.err.Error(), "invalid credentials") {
		t.Errorf("after revoking: %v", r.err)
	}
}

func TestLoginCommand(t *testing.T) {
	url, db := serverWithDatabase(t, func(c *config.Config) { c.Auth.DefaultAccess = "deny-all" })
	mustSucceed(t, admin(t, db, secret+"\n", "user", "add", "alice"))
	mustSucceed(t, admin(t, db, "", "access", "grant", "alice", "t", "read-write"))

	r := mustSucceed(t, run(t, bg, secret+"\n", nil, "login", "-s", url, "--label", "phone", "--expires", "7d", "alice"))
	token := strings.TrimSpace(r.stdout)
	if !auth.LooksLikeToken(token) || !strings.Contains(r.stderr, "Logged in as alice") || strings.Contains(r.stderr, token) || strings.Contains(r.stderr+r.stdout, secret) {
		t.Fatalf("stdout = %q, stderr = %q: stdout must hold only the token, and nothing may show the password", r.stdout, r.stderr)
	}
	mustSucceed(t, run(t, bg, "", map[string]string{"FLARE_TOKEN": token}, "publish", "-s", url, "t", "with a token from login"))

	// It is recorded with its label and expiry.
	list := mustSucceed(t, admin(t, db, "", "token", "list", "alice")).stdout
	if !strings.Contains(list, "phone") {
		t.Errorf("token list =\n%s", list)
	}

	for name, tt := range map[string]struct {
		stdin   string
		args    []string
		mention string
	}{
		"a wrong password": {"not the password\n", []string{"login", "-s", url, "alice"}, "invalid credentials"},
		"an unknown user":  {secret + "\n", []string{"login", "-s", url, "nobody"}, "invalid credentials"},
		"no password":      {"", []string{"login", "-s", url, "alice"}, "no password given"},
		"a bad lifetime":   {secret + "\n", []string{"login", "-s", url, "--expires", "soon", "alice"}, "invalid lifetime"},
		"no user":          {secret + "\n", []string{"login", "-s", url}, "accepts 1 arg"},
		"a bad server":     {secret + "\n", []string{"login", "-s", "nope", "alice"}, "invalid server address"},
	} {
		r := run(t, bg, tt.stdin, nil, tt.args...)
		if r.err == nil || !strings.Contains(r.err.Error(), tt.mention) {
			t.Errorf("%s: err = %v, want it to mention %q", name, r.err, tt.mention)
		}
		if r.stdout != "" {
			t.Errorf("%s: printed %q on stdout", name, r.stdout)
		}
	}
}
