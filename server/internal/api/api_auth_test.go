package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/message"
)

const pw = "correct horse battery"

func basicAuth(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

var bg = context.Background()

func (e *env) addUser(name string, role auth.Role) auth.User {
	e.t.Helper()
	u, err := e.auth.CreateUser(bg, name, pw, role)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) tokenFor(name string) (secret, id string) {
	e.t.Helper()
	u, err := e.auth.FindUser(bg, name)
	if err != nil {
		e.t.Fatal(err)
	}
	secret, tok, err := e.auth.IssueToken(bg, u.ID, "test", 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return secret, tok.ID
}

func (e *env) grant(who, pattern string, access auth.Access) {
	e.t.Helper()
	if err := e.auth.Grant(bg, who, pattern, access); err != nil {
		e.t.Fatal(err)
	}
}

func bearer(secret string) []string { return []string{"Authorization", "Bearer " + secret} }

func denyAll() func(*config.Config) { return access(auth.DenyAll) }

func fromProxyClient(ip string) []string { return []string{"X-Forwarded-For", ip} }

func decodeInto(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("not the expected JSON: %q: %v", data, err)
	}
}

// --- who is told what --------------------------------------------------------

func TestAnonymousVisitorsAreAskedToLogInWhereTheRulesRequireIt(t *testing.T) {
	e := newEnv(t, denyAll())
	check := func(what string, resp *http.Response, data []byte) {
		t.Helper()
		e.expectError(resp, data, apierr.Unauthorized, "log in")
		if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") || strings.Contains(got, "Basic") {
			t.Errorf("%s: WWW-Authenticate = %q; it must name Bearer and not Basic, which would make browsers pop up a password dialog", what, got)
		}
	}
	resp, data := e.do("POST", "/t", "x")
	check("publish", resp, data)
	resp, data = e.do("POST", "/v1/messages", `{"topic":"t","message":"x"}`)
	check("publish as JSON", resp, data)
	resp, data = e.do("GET", "/t/json?poll=1", "")
	check("poll", resp, data)
	_, body, cancel := openStream(t, e.srv.URL+"/t/sse")
	defer cancel()
	raw, _ := io.ReadAll(body)
	if !strings.Contains(string(raw), `"code":40101`) {
		t.Errorf("sse: %s", raw)
	}
	resp, data = e.do("GET", "/v1/account", "")
	check("account", resp, data)
}

func TestUsersGetWhatTheirRulesGive(t *testing.T) {
	e := newEnv(t, denyAll())
	e.addUser("alice", auth.RoleUser)
	e.addUser("bob", auth.RoleUser)
	e.grant("alice", "alerts-*", auth.ReadWrite)
	e.grant("bob", "alerts-*", auth.ReadOnly)
	e.grant(auth.Everyone, "status", auth.ReadOnly)
	aliceToken, _ := e.tokenFor("alice")

	publish := func(topic string, headers ...string) (*http.Response, []byte) {
		return e.do("POST", "/"+topic, "hello", headers...)
	}
	// Alice, by password and by token.
	if resp, data := publish("alerts-db", "Authorization", basicAuth("alice", pw)); resp.StatusCode != 200 {
		t.Errorf("alice with her password: %d %s", resp.StatusCode, data)
	}
	if resp, data := publish("alerts-db", bearer(aliceToken)...); resp.StatusCode != 200 {
		t.Errorf("alice with her token: %d %s", resp.StatusCode, data)
	}
	// A logged-in user who is not allowed is forbidden, not asked to log in again.
	resp, data := publish("other", bearer(aliceToken)...)
	e.expectError(resp, data, apierr.Forbidden, "may not write")
	if resp.Header.Get("WWW-Authenticate") != "" {
		t.Error("a 403 should not invite the client to authenticate again")
	}
	resp, data = publish("alerts-db", "Authorization", basicAuth("bob", pw))
	e.expectError(resp, data, apierr.Forbidden, "may not write")
	// Bob can read what Alice published.
	if got := e.poll("/alerts-db/json?poll=1", "Authorization", basicAuth("bob", pw)); len(got) != 2 {
		t.Errorf("bob reads %d messages, want 2", len(got))
	}
	// Everyone may read "status" (an anonymous poll must succeed), and nobody has
	// been given write access to it.
	e.poll("/status/json?poll=1")
	resp, data = publish("status")
	e.expectError(resp, data, apierr.Unauthorized, "log in")
	resp, data = publish("status", "Authorization", basicAuth("bob", pw))
	e.expectError(resp, data, apierr.Forbidden, "may not write")
}

func TestAnAdministratorMayDoEverything(t *testing.T) {
	e := newEnv(t, denyAll())
	e.addUser("root", auth.RoleAdmin)
	if resp, data := e.do("POST", "/anything", "x", "Authorization", basicAuth("root", pw)); resp.StatusCode != 200 {
		t.Errorf("%d %s", resp.StatusCode, data)
	}
	if got := e.poll("/anything/json?poll=1", "Authorization", basicAuth("root", pw)); len(got) != 1 {
		t.Errorf("%d messages", len(got))
	}
}

// trustProxy makes the test server believe X-Forwarded-For from the loopback
// address its own requests come from, so a test can send each request as a
// different client and keep the failed-login throttle out of the way.
func trustProxy(c *config.Config) { c.TrustedProxies = []string{"127.0.0.1"} }

// asNewClient returns headers that make a request look like it came from a client
// that has not been seen before.
func (e *env) asNewClient() []string {
	e.clients++
	return fromProxyClient(fmt.Sprintf("198.51.100.%d", e.clients))
}

func TestWrongCredentialsAreRefusedEvenWhereAnonymousWouldBeAllowed(t *testing.T) {
	// A client that sends a wrong token must hear about it, not quietly carry on as
	// an anonymous visitor.
	e := newEnv(t, trustProxy) // open: read-write for everyone
	e.addUser("alice", auth.RoleUser)
	secret, id := e.tokenFor("alice")
	e.auth.DeleteToken(bg, id, "") // revoked

	with := func(headers ...string) []string { return append(e.asNewClient(), headers...) }
	for name, headers := range map[string][]string{
		"a revoked token":       bearer(secret),
		"a token never issued":  bearer(auth.NewToken()),
		"a malformed token":     bearer("hello"),
		"a wrong password":      {"Authorization", basicAuth("alice", "not the password")},
		"an unknown user":       {"Authorization", basicAuth("nobody", pw)},
		"an unsupported scheme": {"Authorization", "Digest nonsense"},
	} {
		resp, data := e.do("POST", "/t", "x", with(headers...)...)
		e.expectError(resp, data, apierr.InvalidCredentials, "invalid credentials")
		if resp.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s: a 401 must say how to authenticate", name)
		}
	}
	for _, path := range []string{"/t/json?poll=1", "/t/json", "/t/sse", "/v1/account", "/v1/account/tokens", "/v1/ws"} {
		resp, data := e.do("GET", path, "", with(bearer(secret)...)...)
		e.expectError(resp, data, apierr.InvalidCredentials, "invalid credentials")
	}
	resp, data := e.do("POST", "/v1/messages", `{"topic":"t"}`, with(bearer(secret)...)...)
	e.expectError(resp, data, apierr.InvalidCredentials, "invalid credentials")
	if got := e.poll("/t/json?poll=1"); len(got) != 0 {
		t.Errorf("a request with bad credentials stored %d messages", len(got))
	}
}

func TestBadCredentialsAreAnsweredBeforeTheBodyIsRead(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do("POST", "/v1/messages", strings.Repeat("x", 10_000), bearer("junk")...)
	e.expectError(resp, data, apierr.InvalidCredentials, "invalid credentials")
}

// --- throttling --------------------------------------------------------------

func TestTooManyFailedLoginsAreThrottledPerClient(t *testing.T) {
	// Behind a proxy that is trusted, the client is whoever the proxy says.
	e := newEnv(t, func(c *config.Config) { c.TrustedProxies = []string{"127.0.0.1"} })
	e.addUser("alice", auth.RoleUser)
	attacker, bystander := fromProxyClient("203.0.113.66"), fromProxyClient("203.0.113.77")

	for range 10 {
		resp, _ := e.do("POST", "/t", "x", append(attacker, "Authorization", basicAuth("alice", "guess"))...)
		if resp.StatusCode != 401 {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	resp, data := e.do("POST", "/t", "x", append(attacker, "Authorization", basicAuth("alice", pw))...) // right, but too late
	e.expectError(resp, data, apierr.Throttled, "too many failed attempts")
	seconds, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || seconds < 1 || seconds > 600 {
		t.Errorf("Retry-After = %q, want a number of seconds up to 600", resp.Header.Get("Retry-After"))
	}
	if resp, data := e.do("POST", "/t", "x", append(bystander, "Authorization", basicAuth("alice", pw))...); resp.StatusCode != 200 {
		t.Errorf("a different client behind the same proxy: %d %s", resp.StatusCode, data)
	}
	// The attacker's anonymous requests still work: only login attempts are refused.
	if resp, _ := e.do("POST", "/t", "x", attacker...); resp.StatusCode != 200 {
		t.Errorf("an anonymous request from the throttled client: %d", resp.StatusCode)
	}
}

func TestAForgedForwardedForHeaderIsIgnoredWithoutATrustedProxy(t *testing.T) {
	e := newEnv(t) // no trusted proxies
	for i := range 10 {
		e.do("POST", "/t", "x", "Authorization", basicAuth("alice", "guess"), "X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i))
	}
	resp, data := e.do("POST", "/t", "x", "Authorization", basicAuth("alice", "guess"), "X-Forwarded-For", "10.9.9.9")
	e.expectError(resp, data, apierr.Throttled, "too many")
}

// --- logging in --------------------------------------------------------------

func TestLogin(t *testing.T) {
	e := newEnv(t, denyAll())
	e.addUser("alice", auth.RoleUser)
	e.grant("alice", "t", auth.ReadWrite)

	resp, data := e.do("POST", "/v1/auth/login", `{"label":"phone","expires_in":3600}`, "Authorization", basicAuth("alice", pw))
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s (Cache-Control %q)", resp.StatusCode, data, resp.Header.Get("Cache-Control"))
	}
	var login struct {
		Token, User, Role, ID, Label string
		Created, Expires             int64
	}
	decodeInto(t, data, &login)
	if !auth.LooksLikeToken(login.Token) || login.User != "alice" || login.Role != "user" || login.Label != "phone" ||
		login.ID == "" || login.Expires != login.Created+3600 {
		t.Errorf("login response = %+v", login)
	}
	if strings.Contains(string(data), pw) {
		t.Error("the response contains the password")
	}
	// The token works, and only for what Alice may do.
	if resp, data := e.do("POST", "/t", "x", bearer(login.Token)...); resp.StatusCode != 200 {
		t.Errorf("publishing with the new token: %d %s", resp.StatusCode, data)
	}

	// The body is optional.
	resp, data = e.do("POST", "/v1/auth/login", "", "Authorization", basicAuth("alice", pw))
	var plain struct{ Token string }
	decodeInto(t, data, &plain)
	if resp.StatusCode != 200 || !auth.LooksLikeToken(plain.Token) {
		t.Errorf("login without a body: %d %s", resp.StatusCode, data)
	}
}

func TestLoginRefusals(t *testing.T) {
	e := newEnv(t)
	e.addUser("alice", auth.RoleUser)
	secret, _ := e.tokenFor("alice")
	login := func(body string, headers ...string) (*http.Response, []byte) {
		return e.do("POST", "/v1/auth/login", body, headers...)
	}
	good := []string{"Authorization", basicAuth("alice", pw)}

	resp, data := login("")
	e.expectError(resp, data, apierr.Unauthorized, "user name and password")
	resp, data = login("", bearer(secret)...) // a token must not mint tokens
	e.expectError(resp, data, apierr.Unauthorized, "user name and password")
	resp, data = login("", "Authorization", basicAuth("alice", "wrong"))
	e.expectError(resp, data, apierr.InvalidCredentials, "invalid credentials")

	for name, body := range map[string]string{
		"JSON that is not":         `{"label":`,
		"an unknown field":         `{"label":"x","admin":true}`,
		"a negative expiry":        `{"expires_in":-1}`,
		"an expiry of a century":   `{"expires_in":3153600000}`,
		"a label that is too long": `{"label":"` + strings.Repeat("l", 65) + `"}`,
		"a label with a newline":   `{"label":"a\nb"}`,
	} {
		resp, data := login(body, good...)
		if resp.StatusCode != 400 {
			t.Errorf("%s: %d %s, want 400", name, resp.StatusCode, data)
		}
	}
}

func TestTheTokenLimitIsReportedAsAConflict(t *testing.T) {
	e := newEnv(t)
	e.addUser("alice", auth.RoleUser)
	var last int
	var data []byte
	for range 51 {
		var resp *http.Response
		resp, data = e.do("POST", "/v1/auth/login", "", "Authorization", basicAuth("alice", pw))
		last = resp.StatusCode
	}
	if last != 409 || !strings.Contains(string(data), `"code":40902`) {
		t.Errorf("the 51st token: %d %s, want 409 with code 40902", last, data)
	}
}

// --- the account -------------------------------------------------------------

func TestAccountInformation(t *testing.T) {
	e := newEnv(t, denyAll())
	e.addUser("alice", auth.RoleUser)
	e.addUser("bob", auth.RoleUser)
	e.addUser("root", auth.RoleAdmin)
	e.grant("alice", "alerts-*", auth.ReadWrite)
	e.grant("bob", "private", auth.ReadWrite)
	e.grant(auth.Everyone, "status", auth.ReadOnly)
	aliceToken, _ := e.tokenFor("alice")

	resp, data := e.do("GET", "/v1/account", "", bearer(aliceToken)...)
	var account struct {
		User, Role    string
		DefaultAccess string `json:"default_access"`
		Access        []struct{ Pattern, Access, Scope string }
	}
	decodeInto(t, data, &account)
	if resp.StatusCode != 200 || account.User != "alice" || account.Role != "user" || account.DefaultAccess != "deny-all" || len(account.Access) != 2 {
		t.Fatalf("%d %+v", resp.StatusCode, account)
	}
	scopes := map[string]string{}
	for _, a := range account.Access {
		scopes[a.Pattern] = a.Scope
	}
	if scopes["alerts-*"] != "user" || scopes["status"] != "everyone" || scopes["private"] != "" {
		t.Errorf("rules = %v: Alice should see her own and the one for everyone, and not Bob's", scopes)
	}

	_, data = e.do("GET", "/v1/account", "", "Authorization", basicAuth("root", pw))
	decodeInto(t, data, &account)
	if account.Role != "admin" {
		t.Errorf("role = %q", account.Role)
	}
}

func TestListingAndRevokingTokens(t *testing.T) {
	e := newEnv(t, denyAll())
	e.addUser("alice", auth.RoleUser)
	e.addUser("bob", auth.RoleUser)
	e.grant("alice", "t", auth.ReadWrite)
	phone, phoneID := e.tokenFor("alice")
	laptop, laptopID := e.tokenFor("alice")
	_, bobID := e.tokenFor("bob")

	resp, data := e.do("GET", "/v1/account/tokens", "", bearer(phone)...)
	var list struct{ Tokens []struct{ ID, Label string } }
	decodeInto(t, data, &list)
	if resp.StatusCode != 200 || len(list.Tokens) != 2 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), phone) || strings.Contains(string(data), auth.HashToken(phone)) {
		t.Error("the list contains a token's secret or its hash")
	}

	// Another user's token is reported as missing, so that it can't be probed for.
	resp, data = e.do("DELETE", "/v1/account/tokens/"+bobID, "", bearer(phone)...)
	e.expectError(resp, data, apierr.NotFound, "no such token")
	resp, data = e.do("DELETE", "/v1/account/tokens/nonsense", "", bearer(phone)...)
	e.expectError(resp, data, apierr.NotFound, "no such token")

	// Revoke the laptop's token, using the phone's.
	if resp, _ := e.do("DELETE", "/v1/account/tokens/"+laptopID, "", bearer(phone)...); resp.StatusCode != 204 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp, data = e.do("POST", "/t", "x", bearer(laptop)...)
	e.expectError(resp, data, apierr.InvalidCredentials, "invalid credentials")
	if resp, _ := e.do("POST", "/t", "x", bearer(phone)...); resp.StatusCode != 200 {
		t.Errorf("the other token was affected: %d", resp.StatusCode)
	}
	// A device can log itself out.
	if resp, _ := e.do("DELETE", "/v1/account/tokens/"+phoneID, "", bearer(phone)...); resp.StatusCode != 204 {
		t.Errorf("logging out: %d", resp.StatusCode)
	}
	if resp, _ := e.do("GET", "/v1/account", "", bearer(phone)...); resp.StatusCode != 401 {
		t.Errorf("after logging out: %d, want 401", resp.StatusCode)
	}
}

func TestAccountEndpointsNeedALogin(t *testing.T) {
	e := newEnv(t) // an open server: anonymous visitors may publish, but have no account
	for _, tt := range []struct{ method, path string }{
		{"GET", "/v1/account"}, {"GET", "/v1/account/tokens"}, {"DELETE", "/v1/account/tokens/x"},
	} {
		resp, data := e.do(tt.method, tt.path, "")
		e.expectError(resp, data, apierr.Unauthorized, "log in")
	}
}

// --- open connections --------------------------------------------------------

// streamEnds reads a stream until it ends and says how. It fails the test if the
// stream is still open after a few seconds.
func streamEnds(t *testing.T, body io.Reader) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, body)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the stream stayed open after access was withdrawn")
		return nil
	}
}

func TestOpenStreamsEndWhenAccessIsWithdrawn(t *testing.T) {
	withdrawals := map[string]func(e *env, tokenID string){
		"the token is revoked": func(e *env, tokenID string) { e.auth.DeleteToken(bg, tokenID, "") },
		"the user is deleted":  func(e *env, _ string) { e.auth.DeleteUser(bg, "alice") },
		"the rule is revoked":  func(e *env, _ string) { e.auth.Revoke(bg, "alice", "t") },
	}
	for _, kind := range []string{"json", "sse"} {
		for name, withdraw := range withdrawals {
			t.Run(kind+"/"+name, func(t *testing.T) {
				e := newEnv(t, denyAll())
				e.addUser("alice", auth.RoleUser)
				e.grant("alice", "t", auth.ReadOnly)
				secret, tokenID := e.tokenFor("alice")

				resp, body, _ := openStream(t, e.srv.URL+"/t/"+kind, bearer(secret)...)
				if resp.StatusCode != 200 {
					t.Fatalf("status %d", resp.StatusCode)
				}
				// While access lasts, the stream carries messages (and survives many
				// heartbeats, each of which re-checks).
				time.Sleep(150 * time.Millisecond)
				sent := e.publishedByHub("t", "while allowed")
				seen := false
				for !seen {
					line, err := body.ReadString('\n')
					if err != nil {
						t.Fatalf("the stream ended while access was still granted: %v", err)
					}
					seen = strings.Contains(line, sent)
				}

				withdraw(e, tokenID)
				if err := streamEnds(t, body); err != nil {
					t.Errorf("the stream ended with an error instead of cleanly: %v", err)
				}
				// And it cannot simply be reopened.
				resp, data := e.do("GET", "/t/"+kind+"?since=all", "", bearer(secret)...)
				if resp.StatusCode != 401 && resp.StatusCode != 403 {
					t.Errorf("reconnecting after access was withdrawn: %d %s", resp.StatusCode, data)
				}
			})
		}
	}
}

func TestAStreamOpenedWithAPasswordEndsWhenThePasswordChanges(t *testing.T) {
	e := newEnv(t, denyAll())
	e.addUser("alice", auth.RoleUser)
	e.grant("alice", "t", auth.ReadOnly)
	_, body, _ := openStream(t, e.srv.URL+"/t/json", "Authorization", basicAuth("alice", pw))
	body.ReadString('\n')
	time.Sleep(100 * time.Millisecond)
	if err := e.auth.SetPassword(bg, "alice", "a completely new password"); err != nil {
		t.Fatal(err)
	}
	streamEnds(t, body)
}

func (e *env) publishedByHub(topic, body string) string {
	e.t.Helper()
	if _, err := e.hub.Publish(bg, message.Draft{Topic: topic, Body: body}); err != nil {
		e.t.Fatal(err)
	}
	return body
}

// --- WebSockets --------------------------------------------------------------

func dialWS(t *testing.T, e *env, headers ...string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	for i := 0; i+1 < len(headers); i += 2 {
		h.Set(headers[i], headers[i+1])
	}
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/v1/ws", &websocket.DialOptions{HTTPHeader: h})
	if conn != nil {
		t.Cleanup(func() { conn.CloseNow() })
	}
	return conn, resp, err
}

func wsSend(t *testing.T, c *websocket.Conn, v string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(v)); err != nil {
		t.Fatal(err)
	}
}

func wsRead(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("reading a frame: %v", err)
	}
	var out map[string]any
	decodeInto(t, data, &out)
	return out
}

func TestWebSocketLoginWithAHeader(t *testing.T) {
	e := newEnv(t, denyAll())
	e.addUser("alice", auth.RoleUser)
	e.grant("alice", "t", auth.ReadWrite)
	secret, _ := e.tokenFor("alice")

	conn, _, err := dialWS(t, e, bearer(secret)...)
	if err != nil {
		t.Fatal(err)
	}
	wsSend(t, conn, `{"op":"subscribe","topic":"t"}`)
	if f := wsRead(t, conn); f["event"] != "subscribed" {
		t.Fatalf("got %v", f)
	}
	wsSend(t, conn, `{"op":"publish","topic":"t","message":"hi"}`)
	// Alice is subscribed to what she published to, so the message comes back too.
	got := map[string]bool{}
	for len(got) < 2 {
		got[fmt.Sprint(wsRead(t, conn)["event"])] = true
	}
	if !got["published"] || !got["message"] {
		t.Errorf("got %v, want both the acknowledgement and the message", got)
	}
	wsSend(t, conn, `{"op":"subscribe","topic":"other"}`)
	if f := wsRead(t, conn); f["event"] != "error" || f["code"] != float64(apierr.Forbidden) {
		t.Errorf("a topic Alice has no rule for: %v, want forbidden", f)
	}

	// Without credentials the same connection is an anonymous visitor.
	anon, _, err := dialWS(t, e)
	if err != nil {
		t.Fatal(err)
	}
	wsSend(t, anon, `{"op":"subscribe","topic":"t"}`)
	if f := wsRead(t, anon); f["code"] != float64(apierr.Unauthorized) {
		t.Errorf("anonymous: %v, want an error telling it to log in", f)
	}
}

func TestWebSocketRefusesBadCredentialsInTheUpgrade(t *testing.T) {
	e := newEnv(t)
	_, resp, err := dialWS(t, e, bearer(auth.NewToken())...)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("err = %v, resp = %v; want the upgrade refused with 401", err, resp)
	}
}

func TestWebSocketLoginWithAFrame(t *testing.T) {
	// How a browser logs in: it can't set headers on a WebSocket.
	e := newEnv(t, denyAll())
	e.addUser("alice", auth.RoleUser)
	e.grant("alice", "t", auth.ReadWrite)
	secret, _ := e.tokenFor("alice")

	conn, _, err := dialWS(t, e)
	if err != nil {
		t.Fatal(err)
	}
	wsSend(t, conn, `{"op":"auth","token":"flr_not-a-token"}`)
	if f := wsRead(t, conn); f["code"] != float64(apierr.InvalidCredentials) {
		t.Errorf("a bad token: %v", f)
	}
	wsSend(t, conn, `{"op":"auth","token":"`+secret+`"}`)
	if f := wsRead(t, conn); f["event"] != "authenticated" || f["user"] != "alice" {
		t.Fatalf("a good token: %v", f)
	}
	wsSend(t, conn, `{"op":"subscribe","topic":"t"}`)
	if f := wsRead(t, conn); f["event"] != "subscribed" {
		t.Errorf("after logging in: %v", f)
	}
}

func TestWebSocketLoginFailuresAreThrottled(t *testing.T) {
	e := newEnv(t)
	conn, _, err := dialWS(t, e)
	if err != nil {
		t.Fatal(err)
	}
	var last map[string]any
	for range 11 {
		wsSend(t, conn, `{"op":"auth","token":"`+auth.NewToken()+`"}`)
		last = wsRead(t, conn)
	}
	if last["code"] != float64(apierr.Throttled) {
		t.Errorf("the 11th guess: %v, want it throttled", last)
	}
}

func TestWebSocketIsClosedWhenItsTokenIsRevoked(t *testing.T) {
	e := newEnv(t, denyAll())
	e.addUser("alice", auth.RoleUser)
	e.grant("alice", "t", auth.ReadOnly)
	secret, id := e.tokenFor("alice")

	conn, _, err := dialWS(t, e, bearer(secret)...)
	if err != nil {
		t.Fatal(err)
	}
	wsSend(t, conn, `{"op":"subscribe","topic":"t"}`)
	wsRead(t, conn)
	e.auth.DeleteToken(bg, id, "")

	// Reading is also what lets the client answer the server's pings.
	for {
		ctx, cancel := context.WithTimeout(bg, 5*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Errorf("closed with %v (status %d), want a policy violation", err, websocket.CloseStatus(err))
			}
			return
		}
		if strings.Contains(string(data), `"code":40102`) {
			continue // the error that comes just before the close
		}
	}
}

// --- what is not logged ------------------------------------------------------

func TestCredentialsNeverReachTheLogs(t *testing.T) {
	e := newEnv(t, trustProxy)
	e.addUser("alice", auth.RoleUser)
	secret, _ := e.tokenFor("alice")
	e.do("POST", "/t", "x", bearer(secret)...)
	e.do("POST", "/t", "x", "Authorization", basicAuth("alice", pw))
	e.do("POST", "/t", "x", append(e.asNewClient(), "Authorization", basicAuth("alice", "a wrong password"))...)
	e.do("POST", "/v1/auth/login", "", "Authorization", basicAuth("alice", pw))
	e.do("POST", "/t", "x", append(e.asNewClient(), bearer(auth.NewToken())...)...)

	logs := e.logs.String()
	for _, secretText := range []string{secret, pw, "a wrong password", "Bearer", "Basic", "Authorization", "flr_", base64.StdEncoding.EncodeToString([]byte("alice:" + pw))} {
		if strings.Contains(logs, secretText) {
			t.Errorf("the logs contain %q:\n%s", secretText, logs)
		}
	}
}
