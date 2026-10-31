package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
)

var ctx = context.Background()

func user(id, name string, role auth.Role) auth.User {
	return auth.User{ID: id, Name: name, Role: role, CreatedAt: time.Unix(1_800_000_000, 0)}
}

func addUser(t *testing.T, s auth.Accounts, id, name string) auth.User {
	t.Helper()
	u := user(id, name, auth.RoleUser)
	if err := s.CreateUser(ctx, u, "hash-of-"+name); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCreateAndFindUsers(t *testing.T) {
	s := open(t)
	alice := user("u1", "alice", auth.RoleAdmin)
	if err := s.CreateUser(ctx, alice, "alice-hash"); err != nil {
		t.Fatal(err)
	}

	got, hash, err := s.UserByName(ctx, "alice")
	if err != nil || !reflect.DeepEqual(got, alice) || hash != "alice-hash" {
		t.Errorf("UserByName = %+v, %q, %v", got, hash, err)
	}
	got, hash, err = s.UserByID(ctx, "u1")
	if err != nil || !reflect.DeepEqual(got, alice) || hash != "alice-hash" {
		t.Errorf("UserByID = %+v, %q, %v", got, hash, err)
	}
	for _, missing := range []string{"bob", "ALICE", "", "alice "} {
		if _, _, err := s.UserByName(ctx, missing); !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("UserByName(%q) = %v, want ErrNotFound", missing, err)
		}
	}
	if _, _, err := s.UserByID(ctx, "nope"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("UserByID = %v, want ErrNotFound", err)
	}
}

func TestUserNamesAndIDsMustBeUnique(t *testing.T) {
	s := open(t)
	addUser(t, s, "u1", "alice")
	if err := s.CreateUser(ctx, user("u2", "alice", auth.RoleUser), "h"); !errors.Is(err, auth.ErrExists) {
		t.Errorf("same name: %v, want ErrExists", err)
	}
	if err := s.CreateUser(ctx, user("u1", "bob", auth.RoleUser), "h"); !errors.Is(err, auth.ErrExists) {
		t.Errorf("same ID: %v, want ErrExists", err)
	}
	if err := s.CreateUser(ctx, user("u3", "carol", "root"), "h"); err == nil || errors.Is(err, auth.ErrExists) {
		t.Errorf("an unknown role: %v, want a different error (the database refuses it)", err)
	}
}

func TestListUsersByName(t *testing.T) {
	s := open(t)
	for _, n := range []string{"carol", "alice", "bob"} {
		addUser(t, s, "id-"+n, n)
	}
	users, err := s.ListUsers(ctx)
	if err != nil || len(users) != 3 || users[0].Name != "alice" || users[2].Name != "carol" {
		t.Errorf("ListUsers = %+v, %v", users, err)
	}
	empty, err := open(t).ListUsers(ctx)
	if err != nil || len(empty) != 0 {
		t.Errorf("ListUsers on an empty store = %v, %v", empty, err)
	}
}

func TestSetPasswordAndDeleteUser(t *testing.T) {
	s := open(t)
	addUser(t, s, "u1", "alice")
	if err := s.SetPassword(ctx, "u1", "new-hash"); err != nil {
		t.Fatal(err)
	}
	if _, hash, _ := s.UserByID(ctx, "u1"); hash != "new-hash" {
		t.Errorf("hash = %q", hash)
	}
	if err := s.SetPassword(ctx, "ghost", "x"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("SetPassword for nobody = %v", err)
	}
	if err := s.DeleteUser(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, "u1"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("deleting twice = %v", err)
	}
	if _, _, err := s.UserByID(ctx, "u1"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("the user is still there: %v", err)
	}
}

func TestTokens(t *testing.T) {
	s := open(t)
	alice := addUser(t, s, "u1", "alice")
	expires := time.Unix(1_900_000_000, 0)
	tok := auth.Token{ID: "t1", UserID: alice.ID, Label: "phone", CreatedAt: time.Unix(1_800_000_100, 0), ExpiresAt: &expires}
	if err := s.CreateToken(ctx, tok, "hash-1"); err != nil {
		t.Fatal(err)
	}

	got, owner, err := s.TokenByHash(ctx, "hash-1")
	if err != nil || !reflect.DeepEqual(got, tok) || !reflect.DeepEqual(owner, alice) {
		t.Errorf("TokenByHash = %+v, %+v, %v", got, owner, err)
	}
	if _, _, err := s.TokenByHash(ctx, "other-hash"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("an unknown hash: %v", err)
	}
	if err := s.CreateToken(ctx, auth.Token{ID: "t2", UserID: alice.ID, CreatedAt: time.Now()}, "hash-1"); err == nil {
		t.Error("two tokens with the same hash were accepted")
	}

	// A token that never expires and was never used round-trips as nil.
	if err := s.CreateToken(ctx, auth.Token{ID: "t3", UserID: alice.ID, CreatedAt: time.Unix(1_800_000_200, 0)}, "hash-3"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.TokenByHash(ctx, "hash-3")
	if got.ExpiresAt != nil || got.LastUsedAt != nil {
		t.Errorf("expires %v, last used %v; want neither", got.ExpiresAt, got.LastUsedAt)
	}

	used := time.Unix(1_800_000_500, 0)
	if err := s.TouchToken(ctx, "t1", used); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.TokenByHash(ctx, "hash-1")
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Errorf("last used = %v, want %v", got.LastUsedAt, used)
	}

	list, err := s.ListTokens(ctx, alice.ID)
	if err != nil || len(list) != 2 || list[0].ID != "t1" || list[1].ID != "t3" {
		t.Errorf("ListTokens = %+v, %v", list, err)
	}
	if other, _ := s.ListTokens(ctx, "nobody"); len(other) != 0 {
		t.Errorf("another user's list = %+v", other)
	}
}

func TestATokenNeedsAnExistingUser(t *testing.T) {
	s := open(t)
	err := s.CreateToken(ctx, auth.Token{ID: "t1", UserID: "ghost", CreatedAt: time.Now()}, "h")
	if err == nil {
		t.Error("a token for a user who doesn't exist was accepted: foreign keys are not enforced")
	}
}

func TestDeleteToken(t *testing.T) {
	s := open(t)
	alice, bob := addUser(t, s, "u1", "alice"), addUser(t, s, "u2", "bob")
	s.CreateToken(ctx, auth.Token{ID: "ta", UserID: alice.ID, CreatedAt: time.Now()}, "ha")
	s.CreateToken(ctx, auth.Token{ID: "tb", UserID: bob.ID, CreatedAt: time.Now()}, "hb")

	if err := s.DeleteToken(ctx, "ta", bob.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("bob deleting alice's token: %v, want ErrNotFound (and the token must survive)", err)
	}
	if _, _, err := s.TokenByHash(ctx, "ha"); err != nil {
		t.Errorf("alice's token was deleted by someone else: %v", err)
	}
	if err := s.DeleteToken(ctx, "ta", alice.ID); err != nil {
		t.Errorf("alice deleting her own: %v", err)
	}
	if err := s.DeleteToken(ctx, "tb", ""); err != nil { // an administrator may delete any
		t.Errorf("deleting without an owner: %v", err)
	}
	if err := s.DeleteToken(ctx, "tb", ""); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
}

func TestDeletingAUserDeletesTheirTokensAndRules(t *testing.T) {
	s := open(t)
	alice, bob := addUser(t, s, "u1", "alice"), addUser(t, s, "u2", "bob")
	s.CreateToken(ctx, auth.Token{ID: "ta", UserID: alice.ID, CreatedAt: time.Now()}, "ha")
	s.CreateToken(ctx, auth.Token{ID: "tb", UserID: bob.ID, CreatedAt: time.Now()}, "hb")
	s.GrantAccess(ctx, auth.Rule{UserID: alice.ID, Pattern: "a", Access: auth.ReadWrite})
	s.GrantAccess(ctx, auth.Rule{UserID: bob.ID, Pattern: "b", Access: auth.ReadOnly})
	s.GrantAccess(ctx, auth.Rule{Pattern: "public", Access: auth.ReadOnly})

	if err := s.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TokenByHash(ctx, "ha"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("the deleted user's token still works: %v", err)
	}
	rules, _ := s.ListRules(ctx)
	if len(rules) != 2 {
		t.Errorf("rules after the delete: %+v, want bob's and the one for everyone", rules)
	}
	if _, _, err := s.TokenByHash(ctx, "hb"); err != nil {
		t.Errorf("another user's token was lost: %v", err)
	}
}

func TestGrantAccessReplacesTheRuleForTheSamePattern(t *testing.T) {
	s := open(t)
	alice := addUser(t, s, "u1", "alice")
	s.GrantAccess(ctx, auth.Rule{UserID: alice.ID, Pattern: "t", Access: auth.ReadOnly})
	s.GrantAccess(ctx, auth.Rule{UserID: alice.ID, Pattern: "t", Access: auth.ReadWrite})
	s.GrantAccess(ctx, auth.Rule{UserID: alice.ID, Pattern: "u", Access: auth.DenyAll})

	rules, _ := s.RulesFor(ctx, alice.ID)
	want := []auth.Rule{
		{UserID: alice.ID, Pattern: "t", Access: auth.ReadWrite},
		{UserID: alice.ID, Pattern: "u", Access: auth.DenyAll},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Errorf("rules = %+v, want %+v", rules, want)
	}
}

func TestRulesForEveryoneAreAlsoUnique(t *testing.T) {
	// A plain UNIQUE index treats NULLs as distinct, which would allow duplicates
	// of the rules that have no user.
	s := open(t)
	for _, a := range []auth.Access{auth.ReadOnly, auth.ReadWrite, auth.DenyAll} {
		if err := s.GrantAccess(ctx, auth.Rule{Pattern: "public", Access: a}); err != nil {
			t.Fatal(err)
		}
	}
	rules, _ := s.ListRules(ctx)
	if len(rules) != 1 || rules[0].Access != auth.DenyAll || rules[0].UserID != "" {
		t.Errorf("rules = %+v, want the one latest rule for everyone", rules)
	}
}

func TestRulesForAUserAreTheirOwnPlusEveryones(t *testing.T) {
	s := open(t)
	alice, bob := addUser(t, s, "u1", "alice"), addUser(t, s, "u2", "bob")
	s.GrantAccess(ctx, auth.Rule{UserID: alice.ID, Pattern: "a", Access: auth.ReadWrite})
	s.GrantAccess(ctx, auth.Rule{UserID: bob.ID, Pattern: "b", Access: auth.ReadWrite})
	s.GrantAccess(ctx, auth.Rule{Pattern: "public", Access: auth.ReadOnly})

	patterns := func(rules []auth.Rule) []string {
		var out []string
		for _, r := range rules {
			out = append(out, r.UserID+":"+r.Pattern)
		}
		return out
	}
	forAlice, _ := s.RulesFor(ctx, alice.ID)
	if got := patterns(forAlice); !reflect.DeepEqual(got, []string{"u1:a", ":public"}) {
		t.Errorf("alice's rules = %v: bob's rule must not appear", got)
	}
	anonymous, _ := s.RulesFor(ctx, "")
	if got := patterns(anonymous); !reflect.DeepEqual(got, []string{":public"}) {
		t.Errorf("an anonymous visitor's rules = %v: only the ones for everyone", got)
	}
	all, _ := s.ListRules(ctx)
	if len(all) != 3 || all[0].UserID != "" {
		t.Errorf("ListRules = %+v, want 3 with the ones for everyone first", all)
	}
}

func TestRevokeAccess(t *testing.T) {
	s := open(t)
	alice := addUser(t, s, "u1", "alice")
	s.GrantAccess(ctx, auth.Rule{UserID: alice.ID, Pattern: "t", Access: auth.ReadWrite})
	s.GrantAccess(ctx, auth.Rule{Pattern: "t", Access: auth.ReadOnly})

	if err := s.RevokeAccess(ctx, alice.ID, "t"); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAccess(ctx, alice.ID, "t"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("revoking twice: %v", err)
	}
	rules, _ := s.ListRules(ctx)
	if len(rules) != 1 || rules[0].UserID != "" {
		t.Errorf("rules = %+v: only alice's own rule should have gone", rules)
	}
	if err := s.RevokeAccess(ctx, "", "t"); err != nil {
		t.Errorf("revoking the rule for everyone: %v", err)
	}
}

func TestTheDatabaseRefusesAnUnknownAccessLevel(t *testing.T) {
	s := open(t)
	if err := s.GrantAccess(ctx, auth.Rule{Pattern: "t", Access: "admin"}); err == nil {
		t.Error("an unknown access level was stored")
	}
}

func TestAccountsSurviveReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flare.db")
	s, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	alice := addUser(t, s, "u1", "alice")
	s.CreateToken(ctx, auth.Token{ID: "t1", UserID: alice.ID, CreatedAt: time.Now()}, "hash")
	s.GrantAccess(ctx, auth.Rule{UserID: alice.ID, Pattern: "t", Access: auth.ReadWrite})
	s.Close()

	s, err = OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.TokenByHash(ctx, "hash"); err != nil {
		t.Errorf("the token was lost: %v", err)
	}
	if rules, _ := s.RulesFor(ctx, alice.ID); len(rules) != 1 {
		t.Errorf("rules = %+v", rules)
	}
}

func TestConcurrentAccountChanges(t *testing.T) {
	s := open(t)
	alice := addUser(t, s, "u1", "alice")
	var wg sync.WaitGroup
	for g := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 20 {
				tok := auth.Token{ID: fmt.Sprintf("t%d-%d", g, i), UserID: alice.ID, CreatedAt: time.Now()}
				if err := s.CreateToken(ctx, tok, fmt.Sprintf("h%d-%d", g, i)); err != nil {
					t.Error(err)
					return
				}
				if err := s.GrantAccess(ctx, auth.Rule{UserID: alice.ID, Pattern: fmt.Sprintf("p%d", i%5), Access: auth.ReadWrite}); err != nil {
					t.Error(err)
					return
				}
				s.RulesFor(ctx, alice.ID)
				s.TouchToken(ctx, tok.ID, time.Now())
			}
		}()
	}
	wg.Wait()
	if tokens, _ := s.ListTokens(ctx, alice.ID); len(tokens) != 120 {
		t.Errorf("%d tokens, want 120", len(tokens))
	}
	if rules, _ := s.RulesFor(ctx, alice.ID); len(rules) != 5 {
		t.Errorf("%d rules, want 5 (one per pattern)", len(rules))
	}
}
