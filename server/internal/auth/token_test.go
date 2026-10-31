package auth

import (
	"strings"
	"testing"
)

func TestNewTokenHasTheExpectedForm(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		tok := NewToken()
		if !LooksLikeToken(tok) || !strings.HasPrefix(tok, "flr_") || len(tok) != 47 {
			t.Fatalf("token %q has the wrong form", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q was generated twice", tok)
		}
		seen[tok] = true
	}
}

func TestHashTokenIsStableAndHidesTheToken(t *testing.T) {
	tok := NewToken()
	h := HashToken(tok)
	if h != HashToken(tok) || len(h) != 64 {
		t.Errorf("hash %q: want 64 stable hex characters", h)
	}
	if strings.Contains(h, tok[4:12]) || h == HashToken(NewToken()) {
		t.Error("the hash reveals the token, or two tokens share one")
	}
	if HashToken("") == HashToken("a") {
		t.Error("distinct inputs share a hash")
	}
}

func TestLooksLikeToken(t *testing.T) {
	good := NewToken()
	for _, bad := range []string{
		"", "flr_", good[:46], good + "x", "xlr_" + good[4:], strings.ToUpper(good),
		"flr_" + strings.Repeat("!", 43), "flr_" + strings.Repeat("a", 43) + "\n", " " + good, "Bearer " + good,
	} {
		if LooksLikeToken(bad) {
			t.Errorf("LooksLikeToken(%q) = true", bad)
		}
	}
	if !LooksLikeToken("flr_"+strings.Repeat("a", 43)) || !LooksLikeToken("flr_"+strings.Repeat("-", 43)) || !LooksLikeToken("flr_"+strings.Repeat("_", 43)) {
		t.Error("a well-formed token was refused")
	}
}

func TestNewIDIsRandom(t *testing.T) {
	a, b := newID(), newID()
	if a == b || len(a) != 16 {
		t.Errorf("ids %q, %q", a, b)
	}
}

func TestValidUsername(t *testing.T) {
	for _, ok := range []string{"a", "alice", "bob.smith", "ops-team", "u_1", "0day", strings.Repeat("a", 32)} {
		if err := ValidUsername(ok); err != nil {
			t.Errorf("ValidUsername(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Alice", "a b", "a:b", "a@b", ".dot", "-dash", "_under", "é", strings.Repeat("a", 33), "everyone", "a\n"} {
		if ValidUsername(bad) == nil {
			t.Errorf("ValidUsername(%q) succeeded", bad)
		}
	}
}

func TestParseRole(t *testing.T) {
	for in, want := range map[string]Role{"user": RoleUser, "admin": RoleAdmin} {
		if got, err := ParseRole(in); err != nil || got != want {
			t.Errorf("ParseRole(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "root", "Admin", "users"} {
		if _, err := ParseRole(bad); err == nil {
			t.Errorf("ParseRole(%q) succeeded", bad)
		}
	}
}

func TestIdentity(t *testing.T) {
	var anon Identity
	if !anon.Anonymous() || anon.UserID() != "" {
		t.Error("the zero Identity should be anonymous")
	}
	known := Identity{User: &User{ID: "u1", Name: "alice"}}
	if known.Anonymous() || known.UserID() != "u1" {
		t.Error("an Identity with a user is not anonymous")
	}
}
