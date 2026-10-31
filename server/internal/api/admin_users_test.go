package api

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
)

type usersList struct {
	Users []struct {
		Name     string `json:"name"`
		Role     string `json:"role"`
		Disabled bool   `json:"disabled"`
		Created  int64  `json:"created"`
	} `json:"users"`
}

func (e *env) users(admin []string) usersList {
	e.t.Helper()
	resp, data := e.do("GET", "/v1/admin/users", "", admin...)
	if resp.StatusCode != 200 {
		e.t.Fatalf("GET /v1/admin/users = %d %s", resp.StatusCode, data)
	}
	var out usersList
	decodeInto(e.t, data, &out)
	return out
}

func (l usersList) find(name string) (struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
	Created  int64  `json:"created"`
}, bool) {
	for _, u := range l.Users {
		if u.Name == name {
			return u, true
		}
	}
	return l.Users[0], false
}

const newPassword = "a different long password"

func TestOnlyAdministratorsMayManageUsersTokensAndAccess(t *testing.T) {
	e, admin := newAdminEnv(t)
	alice, _ := e.tokenFor("alice")
	for _, rq := range []struct{ method, path, body string }{
		{"GET", "/v1/admin/users", ""},
		{"POST", "/v1/admin/users", `{"name":"bob","password":"` + pw + `"}`},
		{"PATCH", "/v1/admin/users/alice", `{"role":"admin"}`},
		{"DELETE", "/v1/admin/users/alice", ""},
		{"GET", "/v1/admin/users/alice/tokens", ""},
		{"DELETE", "/v1/admin/users/alice/tokens/x", ""},
		{"GET", "/v1/admin/access", ""},
		{"PUT", "/v1/admin/access", `{"who":"alice","pattern":"*","access":"read-write"}`},
		{"DELETE", "/v1/admin/access?who=alice&pattern=*", ""},
	} {
		resp, data := e.do(rq.method, rq.path, rq.body)
		e.expectError(resp, data, apierr.Unauthorized, "log in")
		resp, data = e.do(rq.method, rq.path, rq.body, bearer(alice)...)
		e.expectError(resp, data, apierr.Forbidden, "administrator")
	}
	if got := e.users(admin); len(got.Users) != 2 {
		t.Errorf("a refused request changed the users: %+v", got)
	}
}

func TestListingUsers(t *testing.T) {
	e, admin := newAdminEnv(t)
	got := e.users(admin)
	root, ok1 := got.find("root")
	alice, ok2 := got.find("alice")
	if !ok1 || !ok2 || root.Role != "admin" || alice.Role != "user" || alice.Disabled || alice.Created == 0 {
		t.Errorf("users = %+v", got)
	}
	for _, u := range got.Users {
		if strings.Contains(fmt.Sprint(u), "password") {
			t.Errorf("a user listing shows a password: %+v", u)
		}
	}
}

func TestCreatingAUser(t *testing.T) {
	e, admin := newAdminEnv(t)
	resp, data := e.do("POST", "/v1/admin/users", `{"name":"bob","password":"`+pw+`"}`, admin...)
	if resp.StatusCode != 201 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), pw) {
		t.Errorf("the answer repeats the password: %s", data)
	}
	if bob, _ := e.users(admin).find("bob"); bob.Role != "user" {
		t.Errorf("the default role is %q", bob.Role)
	}
	if resp, _ := e.do("GET", "/v1/account", "", "Authorization", basicAuth("bob", pw)); resp.StatusCode != 200 {
		t.Errorf("the new user cannot log in: %d", resp.StatusCode)
	}

	resp, data = e.do("POST", "/v1/admin/users", `{"name":"carol","password":"`+pw+`","role":"admin"}`, admin...)
	if resp.StatusCode != 201 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if carol, _ := e.users(admin).find("carol"); carol.Role != "admin" {
		t.Errorf("role = %q", carol.Role)
	}
}

func TestCreatingAUserIsRefusedWithTheReason(t *testing.T) {
	e, admin := newAdminEnv(t)
	for _, tc := range []struct{ what, body, mention string }{
		{"a name with capitals", `{"name":"Bob","password":"` + pw + `"}`, "lower-case"},
		{"a name with a space", `{"name":"b ob","password":"` + pw + `"}`, "lower-case"},
		{"the name everyone", `{"name":"everyone","password":"` + pw + `"}`, "reserved"},
		{"no name", `{"password":"` + pw + `"}`, "user name"},
		{"a short password", `{"name":"bob","password":"short"}`, "at least"},
		{"no password", `{"name":"bob"}`, "at least"},
		{"a role that does not exist", `{"name":"bob","password":"` + pw + `","role":"king"}`, "role must be"},
	} {
		resp, data := e.do("POST", "/v1/admin/users", tc.body, admin...)
		e.expectError(resp, data, apierr.InvalidParameter, tc.mention)
		if resp.StatusCode != 400 {
			t.Errorf("%s: status %d", tc.what, resp.StatusCode)
		}
	}
	resp, data := e.do("POST", "/v1/admin/users", `{"name":"alice","password":"`+pw+`"}`, admin...)
	e.expectError(resp, data, apierr.AlreadyExists, "already exists")
	for _, body := range []string{"", "not json", `{"name":"bob","password":"` + pw + `","admin":true}`} {
		resp, data := e.do("POST", "/v1/admin/users", body, admin...)
		e.expectError(resp, data, apierr.InvalidBody, "")
	}
	if n := len(e.users(admin).Users); n != 2 {
		t.Errorf("%d users after the refusals", n)
	}
}

func TestChangingAUsersRole(t *testing.T) {
	e, admin := newAdminEnv(t)
	resp, data := e.do("PATCH", "/v1/admin/users/alice", `{"role":"admin"}`, admin...)
	if resp.StatusCode != 200 || !strings.Contains(string(data), `"role":"admin"`) {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	// The new administrator can now do what administrators do.
	if resp, _ := e.do("GET", "/v1/admin/users", "", "Authorization", basicAuth("alice", pw)); resp.StatusCode != 200 {
		t.Errorf("a new administrator: %d", resp.StatusCode)
	}
	// And another administrator may take it back.
	if resp, _ := e.do("PATCH", "/v1/admin/users/alice", `{"role":"user"}`, admin...); resp.StatusCode != 200 {
		t.Fatalf("demoting: %d", resp.StatusCode)
	}
	if resp, _ := e.do("GET", "/v1/admin/users", "", "Authorization", basicAuth("alice", pw)); resp.StatusCode != 403 {
		t.Errorf("a demoted administrator: %d", resp.StatusCode)
	}
}

func TestChangingAUsersPassword(t *testing.T) {
	e, admin := newAdminEnv(t)
	resp, data := e.do("PATCH", "/v1/admin/users/alice", `{"password":"`+newPassword+`"}`, admin...)
	if resp.StatusCode != 200 || strings.Contains(string(data), newPassword) {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if resp, _ := e.do("GET", "/v1/account", "", "Authorization", basicAuth("alice", pw)); resp.StatusCode != 401 {
		t.Errorf("the old password: %d", resp.StatusCode)
	}
	if resp, _ := e.do("GET", "/v1/account", "", "Authorization", basicAuth("alice", newPassword)); resp.StatusCode != 200 {
		t.Errorf("the new password: %d", resp.StatusCode)
	}
}

func TestDisablingAUserCutsThemOffAndEnablingRestoresThem(t *testing.T) {
	e, admin := newAdminEnv(t)
	token, _ := e.tokenFor("alice")
	login := func() int {
		resp, _ := e.do("GET", "/v1/account", "", "Authorization", basicAuth("alice", pw))
		return resp.StatusCode
	}
	byToken := func() int { resp, _ := e.do("GET", "/v1/account", "", bearer(token)...); return resp.StatusCode }
	if login() != 200 || byToken() != 200 {
		t.Fatal("alice cannot log in to begin with")
	}

	resp, data := e.do("PATCH", "/v1/admin/users/alice", `{"disabled":true}`, admin...)
	if resp.StatusCode != 200 || !strings.Contains(string(data), `"disabled":true`) {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if login() != 401 || byToken() != 401 {
		t.Errorf("a disabled user: password %d, token %d", login(), byToken())
	}
	// Nothing of theirs is gone.
	resp, data = e.do("GET", "/v1/admin/users/alice/tokens", "", admin...)
	if resp.StatusCode != 200 || !strings.Contains(string(data), `"id"`) {
		t.Errorf("their tokens: %d %s", resp.StatusCode, data)
	}

	e.do("PATCH", "/v1/admin/users/alice", `{"disabled":false}`, admin...)
	if login() != 200 || byToken() != 200 {
		t.Errorf("after enabling: password %d, token %d", login(), byToken())
	}
}

func TestAChangeThatIsRefusedChangesNothing(t *testing.T) {
	e, admin := newAdminEnv(t)
	for _, body := range []string{
		`{"role":"admin","password":"short"}`,
		`{"disabled":true,"role":"king"}`,
		`{"role":"admin","disabled":"yes"}`,
	} {
		resp, _ := e.do("PATCH", "/v1/admin/users/alice", body, admin...)
		if resp.StatusCode != 400 {
			t.Errorf("%s: %d", body, resp.StatusCode)
		}
	}
	got, _ := e.users(admin).find("alice")
	if got.Role != "user" || got.Disabled {
		t.Errorf("a refused request changed alice: %+v", got)
	}
	resp, data := e.do("PATCH", "/v1/admin/users/alice", `{}`, admin...)
	e.expectError(resp, data, apierr.InvalidBody, "at least one")
	resp, data = e.do("PATCH", "/v1/admin/users/nobody", `{"role":"admin"}`, admin...)
	e.expectError(resp, data, apierr.NotFound, "nobody")
}

func TestAnAdministratorCannotLockThemselvesOut(t *testing.T) {
	e, admin := newAdminEnv(t)
	for _, body := range []string{`{"disabled":true}`, `{"role":"user"}`} {
		resp, data := e.do("PATCH", "/v1/admin/users/root", body, admin...)
		e.expectError(resp, data, apierr.Conflict, "yourself")
		if resp.StatusCode != 409 {
			t.Errorf("%s: status %d", body, resp.StatusCode)
		}
	}
	resp, data := e.do("DELETE", "/v1/admin/users/root", "", admin...)
	e.expectError(resp, data, apierr.Conflict, "yourself")

	// What does no harm is allowed: a new password, staying an administrator, being enabled.
	for _, body := range []string{`{"password":"` + newPassword + `"}`, `{"role":"admin"}`, `{"disabled":false}`} {
		if resp, data := e.do("PATCH", "/v1/admin/users/root", body, admin...); resp.StatusCode != 200 {
			t.Errorf("%s: %d %s", body, resp.StatusCode, data)
		}
	}
	if root, _ := e.users(admin).find("root"); root.Role != "admin" || root.Disabled {
		t.Errorf("root = %+v", root)
	}
}

func TestDeletingAUserRemovesTheirTokensAndRules(t *testing.T) {
	e, admin := newAdminEnv(t, denyAll())
	token, _ := e.tokenFor("alice")
	e.do("PUT", "/v1/admin/access", `{"who":"alice","pattern":"alerts","access":"read-write"}`, admin...)

	resp, _ := e.do("DELETE", "/v1/admin/users/alice", "", admin...)
	if resp.StatusCode != 204 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if _, ok := e.users(admin).find("alice"); ok {
		t.Error("alice is still listed")
	}
	if resp, _ := e.do("GET", "/v1/account", "", bearer(token)...); resp.StatusCode != 401 {
		t.Errorf("a deleted user's token: %d", resp.StatusCode)
	}
	_, data := e.do("GET", "/v1/admin/access", "", admin...)
	if strings.Contains(string(data), "alice") {
		t.Errorf("a deleted user's rules remain: %s", data)
	}
	resp, data = e.do("DELETE", "/v1/admin/users/alice", "", admin...)
	e.expectError(resp, data, apierr.NotFound, "alice")
}

func TestListingAndRevokingAUsersTokens(t *testing.T) {
	e, admin := newAdminEnv(t)
	first, firstID := e.tokenFor("alice")
	second, _ := e.tokenFor("alice")

	resp, data := e.do("GET", "/v1/admin/users/alice/tokens", "", admin...)
	if resp.StatusCode != 200 || strings.Contains(string(data), first) || strings.Contains(string(data), second) {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	var list struct {
		Tokens []struct{ ID, Label string } `json:"tokens"`
	}
	decodeInto(t, data, &list)
	if len(list.Tokens) != 2 {
		t.Fatalf("tokens = %+v", list)
	}

	if resp, _ := e.do("DELETE", "/v1/admin/users/alice/tokens/"+firstID, "", admin...); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if resp, _ := e.do("GET", "/v1/account", "", bearer(first)...); resp.StatusCode != 401 {
		t.Errorf("the revoked token: %d", resp.StatusCode)
	}
	if resp, _ := e.do("GET", "/v1/account", "", bearer(second)...); resp.StatusCode != 200 {
		t.Errorf("the other token: %d", resp.StatusCode)
	}
	resp, data = e.do("DELETE", "/v1/admin/users/alice/tokens/"+firstID, "", admin...)
	e.expectError(resp, data, apierr.NotFound, "no such token")
	resp, data = e.do("GET", "/v1/admin/users/nobody/tokens", "", admin...)
	e.expectError(resp, data, apierr.NotFound, "nobody")
}

func TestATokenCannotBeRevokedThroughAnotherUser(t *testing.T) {
	e, admin := newAdminEnv(t)
	e.addUser("bob", auth.RoleUser)
	token, tokenID := e.tokenFor("alice")
	resp, data := e.do("DELETE", "/v1/admin/users/bob/tokens/"+tokenID, "", admin...)
	e.expectError(resp, data, apierr.NotFound, "no such token")
	if resp, _ := e.do("GET", "/v1/account", "", bearer(token)...); resp.StatusCode != 200 {
		t.Errorf("alice's token was revoked through bob: %d", resp.StatusCode)
	}
}

func TestAccessRulesDecideWhatAUserMayDo(t *testing.T) {
	e, admin := newAdminEnv(t, denyAll())
	alice, _ := e.tokenFor("alice")
	poll := func() int {
		resp, _ := e.do("GET", "/alerts-db/json?poll=1", "", bearer(alice)...)
		return resp.StatusCode
	}
	publish := func() int { resp, _ := e.do("POST", "/alerts-db", "x", bearer(alice)...); return resp.StatusCode }
	if poll() != 403 || publish() != 403 {
		t.Fatalf("alice has access to begin with: %d %d", poll(), publish())
	}

	resp, data := e.do("PUT", "/v1/admin/access", `{"who":"alice","pattern":"alerts-*","access":"read-only"}`, admin...)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if poll() != 200 || publish() != 403 {
		t.Errorf("read-only: poll %d, publish %d", poll(), publish())
	}
	e.do("PUT", "/v1/admin/access", `{"who":"alice","pattern":"alerts-*","access":"read-write"}`, admin...) // replaces
	if publish() != 200 {
		t.Errorf("read-write: publish %d", publish())
	}

	_, data = e.do("GET", "/v1/admin/access", "", admin...)
	var list struct {
		Rules []struct{ Who, Pattern, Access string } `json:"rules"`
		Def   string                                  `json:"default_access"`
	}
	decodeInto(t, data, &list)
	if len(list.Rules) != 1 || list.Rules[0].Who != "alice" || list.Rules[0].Access != "read-write" || list.Def != "deny-all" {
		t.Errorf("rules = %+v", list)
	}

	resp, _ = e.do("DELETE", "/v1/admin/access?who=alice&pattern="+url.QueryEscape("alerts-*"), "", admin...)
	if resp.StatusCode != 204 || poll() != 403 {
		t.Errorf("after revoking: %d, poll %d", resp.StatusCode, poll())
	}
}

func TestARuleForEveryone(t *testing.T) {
	e, admin := newAdminEnv(t, denyAll())
	if resp, data := e.do("PUT", "/v1/admin/access", `{"who":"everyone","pattern":"public-*","access":"read-only"}`, admin...); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if resp, _ := e.do("GET", "/public-news/json?poll=1", ""); resp.StatusCode != 200 {
		t.Errorf("an anonymous visitor on a topic open to everyone: %d", resp.StatusCode)
	}
	if resp, _ := e.do("GET", "/private-news/json?poll=1", ""); resp.StatusCode != 401 {
		t.Errorf("another topic: %d", resp.StatusCode)
	}
}

func TestARefusedAccessRule(t *testing.T) {
	e, admin := newAdminEnv(t)
	for _, tc := range []struct {
		body    string
		code    int
		mention string
	}{
		{`{"who":"alice","pattern":"bad pattern!","access":"read-only"}`, apierr.InvalidParameter, "not a valid pattern"},
		{`{"who":"alice","pattern":"alerts","access":"sometimes"}`, apierr.InvalidParameter, "access"},
		{`{"who":"nobody","pattern":"alerts","access":"read-only"}`, apierr.NotFound, "nobody"},
		{`{"who":"alice","pattern":"alerts"}`, apierr.InvalidParameter, "access"},
		{`{"who":"alice","pattern":"alerts","access":"read-only","extra":1}`, apierr.InvalidBody, ""},
	} {
		resp, data := e.do("PUT", "/v1/admin/access", tc.body, admin...)
		e.expectError(resp, data, tc.code, tc.mention)
	}
	resp, data := e.do("DELETE", "/v1/admin/access?who=alice&pattern=alerts", "", admin...)
	e.expectError(resp, data, apierr.NotFound, "no rule")
	resp, data = e.do("DELETE", "/v1/admin/access?who=alice", "", admin...)
	e.expectError(resp, data, apierr.InvalidParameter, "who")
}
