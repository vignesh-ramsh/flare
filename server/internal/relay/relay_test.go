package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/push"
)

var bg = context.Background()

// scripted is a sender whose behaviour a test controls.
type scripted struct {
	mu   sync.Mutex
	sent []push.Notification
	fn   func(push.Notification) error
}

func (s *scripted) MaxPayload() int { return 4000 }
func (s *scripted) Send(_ context.Context, n push.Notification) error {
	s.mu.Lock()
	s.sent = append(s.sent, n)
	fn := s.fn
	s.mu.Unlock()
	if fn != nil {
		return fn(n)
	}
	return nil
}
func (s *scripted) all() []push.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]push.Notification(nil), s.sent...)
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type env struct {
	t       *testing.T
	srv     *httptest.Server
	relay   *Relay
	store   *Store
	fcm     *scripted
	apns    *scripted
	clock   *fakeClock
	logs    *syncBuffer
	senders *push.Registry
}

func newEnv(t *testing.T, edit ...func(*Config)) *env {
	t.Helper()
	st, err := OpenStore(bg, filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{t: t, store: st, fcm: &scripted{}, apns: &scripted{}, clock: &fakeClock{t: time.Unix(1_800_000_000, 0)}, logs: &syncBuffer{}}
	e.senders = push.NewRegistry()
	e.senders.AddApp(push.FCM, "org.example.flare", e.fcm)
	e.senders.AddApp(push.APNs, "org.example.flare", e.apns)
	cfg := Config{
		Senders: e.senders, Store: st, Now: e.clock.Now,
		Log: slog_to(e.logs),
	}
	for _, f := range edit {
		f(&cfg)
	}
	e.relay = New(cfg)
	e.srv = httptest.NewServer(e.relay.Handler())
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(method, path, body string, headers ...string) (*http.Response, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

const (
	fcmToken  = "fcm-registration-token-0123456789abcdef"
	apnsToken = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
)

func fcmBody(token string) string {
	return `{"app_id":"org.example.flare","platform":"android","provider":"fcm","token":"` + token + `"}`
}

func (e *env) register(body string, headers ...string) (handle, secret string) {
	e.t.Helper()
	resp, data := e.do("POST", "/v1/handles", body, headers...)
	if resp.StatusCode != 201 {
		e.t.Fatalf("register: %d %s", resp.StatusCode, data)
	}
	var out struct{ Handle, Secret string }
	json.Unmarshal(data, &out)
	return out.Handle, out.Secret
}

func wakeBody(handle, sub, id string) string {
	return `{"handle":"` + handle + `","sub":"` + sub + `","id":"` + id + `"}`
}

func wantError(t *testing.T, resp *http.Response, data []byte, code int, mention string) {
	t.Helper()
	var body struct {
		Code  int
		Error string
	}
	json.Unmarshal(data, &body)
	if resp.StatusCode != apierr.HTTPStatus(code) || body.Code != code || !strings.Contains(body.Error, mention) {
		t.Errorf("got %d %s; want %d (code %d) mentioning %q", resp.StatusCode, data, apierr.HTTPStatus(code), code, mention)
	}
}

func TestAnAppRegistersATokenAndGetsAHandle(t *testing.T) {
	e := newEnv(t)
	handle, secret := e.register(fcmBody(fcmToken))
	if len(handle) != 32 || !push.ValidHandle(handle) {
		t.Errorf("handle = %q", handle)
	}
	if !strings.HasPrefix(secret, HandleSecretPrefix) || len(secret) != len(HandleSecretPrefix)+43 {
		t.Errorf("secret = %q", secret)
	}
	if other, _ := e.register(fcmBody(fcmToken + "2")); other == handle {
		t.Error("two registrations got the same handle")
	}

	// Only hashes are kept: the database holds nothing that wakes a device.
	if _, err := e.store.Find(bg, handle); !errors.Is(err, ErrNotFound) {
		t.Error("the handle is stored as it is")
	}
	h, err := e.store.Find(bg, push.HashSecret(handle))
	if err != nil {
		t.Fatal(err)
	}
	if h.SecretHash != push.HashSecret(secret) || strings.Contains(h.SecretHash, secret) {
		t.Error("the secret is not stored as a hash")
	}
	if h.Token != fcmToken || h.AppID != "org.example.flare" || h.Provider != push.FCM || h.Platform != "android" {
		t.Errorf("stored %+v", h)
	}

	// APNs tokens are normalised.
	hh, _ := e.register(`{"app_id":"org.example.flare","platform":"ios","provider":"apns","token":"` + strings.ToUpper(apnsToken) + `"}`)
	if got, _ := e.store.Find(bg, push.HashSecret(hh)); got.Token != apnsToken {
		t.Errorf("token = %q", got.Token)
	}
}

func TestRegistrationIsRefusedWithAReason(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.RegistrationsPerIP = 100000 }) // so that the limit is not what refuses them
	tests := []struct {
		name    string
		body    string
		code    int
		mention string
	}{
		{"an unknown field", `{"app_id":"org.example.flare","platform":"android","provider":"fcm","token":"` + fcmToken + `","color":"red"}`, apierr.InvalidBody, "color"},
		{"not JSON", `token=1`, apierr.InvalidBody, "invalid JSON"},
		{"no body", ``, apierr.InvalidBody, "JSON body"},
		{"web push", `{"app_id":"org.example.flare","platform":"web","provider":"webpush","token":"x"}`, apierr.InvalidParameter, "fcm or apns"},
		{"no provider", `{"app_id":"org.example.flare","platform":"android","token":"` + fcmToken + `"}`, apierr.InvalidParameter, "fcm or apns"},
		{"a bad app id", `{"app_id":"my app","platform":"android","provider":"fcm","token":"` + fcmToken + `"}`, apierr.InvalidParameter, "app_id"},
		{"a bad platform", `{"app_id":"org.example.flare","platform":"beos","provider":"fcm","token":"` + fcmToken + `"}`, apierr.InvalidParameter, "platform"},
		{"an app the relay has no keys for", `{"app_id":"com.other.app","platform":"android","provider":"fcm","token":"` + fcmToken + `"}`, apierr.Unsupported, "com.other.app"},
		{"a token that cannot be one", fcmBody("short"), apierr.InvalidParameter, "token"},
		{"an apns token that is not hex", `{"app_id":"org.example.flare","platform":"ios","provider":"apns","token":"zz"}`, apierr.InvalidParameter, "token"},
		{"too large", `{"app_id":"` + strings.Repeat("a", 5000) + `"}`, apierr.TooLarge, "4 KiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, data := e.do("POST", "/v1/handles", tt.body)
			wantError(t, resp, data, tt.code, tt.mention)
		})
	}
	if n, _ := e.store.Count(bg); n != 0 {
		t.Errorf("%d handles stored from refused registrations", n)
	}
}

func TestRegisteringATokenAgainReplacesItsHandle(t *testing.T) {
	e := newEnv(t)
	old, _ := e.register(fcmBody(fcmToken))
	fresh, _ := e.register(fcmBody(fcmToken))
	if n, _ := e.store.Count(bg); n != 1 {
		t.Errorf("%d handles for one token", n)
	}
	resp, data := e.do("POST", "/v1/wake", wakeBody(old, "sub1", "msg1"))
	wantError(t, resp, data, apierr.NotFound, "no such handle")
	if resp, data := e.do("POST", "/v1/wake", wakeBody(fresh, "sub1", "msg1")); resp.StatusCode != 204 {
		t.Errorf("the new handle: %d %s", resp.StatusCode, data)
	}
}

func TestAnAppCanRevokeItsHandleAndNoOneElseCan(t *testing.T) {
	e := newEnv(t)
	handle, secret := e.register(fcmBody(fcmToken))
	_, otherSecret := e.register(fcmBody(fcmToken + "2"))

	resp, data := e.do("DELETE", "/v1/handles/"+handle, "")
	wantError(t, resp, data, apierr.Unauthorized, "Handle-Secret")
	resp, data = e.do("DELETE", "/v1/handles/"+handle, "", HandleSecretHeader, otherSecret)
	wantError(t, resp, data, apierr.InvalidCredentials, "not this handle's")
	resp, data = e.do("DELETE", "/v1/handles/no-such-handle-0123456789", "", HandleSecretHeader, secret)
	wantError(t, resp, data, apierr.NotFound, "no such handle")
	if resp, _ := e.do("POST", "/v1/wake", wakeBody(handle, "s", "m")); resp.StatusCode != 204 {
		t.Fatal("a refused revocation removed the handle")
	}

	if resp, _ := e.do("DELETE", "/v1/handles/"+handle, "", HandleSecretHeader, secret); resp.StatusCode != 204 {
		t.Errorf("revoking: %d", resp.StatusCode)
	}
	resp, data = e.do("POST", "/v1/wake", wakeBody(handle, "s", "m"))
	wantError(t, resp, data, apierr.NotFound, "no such handle")
}

func TestAWakeRequestReachesThePushServiceAsAPollRequest(t *testing.T) {
	e := newEnv(t)
	handle, _ := e.register(fcmBody(fcmToken))
	apnsHandle, _ := e.register(`{"app_id":"org.example.flare","platform":"ios","provider":"apns","token":"` + apnsToken + `"}`)

	resp, data := e.do("POST", "/v1/wake", `{"handle":"`+handle+`","sub":"sub-1","id":"msg-1","priority":5,"ttl":7200}`)
	if resp.StatusCode != 204 || len(data) != 0 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	sent := e.fcm.all()
	if len(sent) != 1 || len(e.apns.all()) != 0 {
		t.Fatalf("fcm got %d, apns got %d", len(sent), len(e.apns.all()))
	}
	n := sent[0]
	if n.Device.Target != fcmToken || n.Device.Provider != push.FCM || n.Device.AppID != "org.example.flare" {
		t.Errorf("device = %+v", n.Device)
	}
	if n.SubscriptionID != "sub-1" || n.Message.ID != "msg-1" || n.Message.Priority != 5 || !n.WakeUp {
		t.Errorf("notification = %+v", n)
	}
	if string(n.Payload) != string(push.WakePayload("sub-1", "msg-1")) {
		t.Errorf("payload = %s", n.Payload)
	}
	if n.TTL() != 2*time.Hour {
		t.Errorf("TTL = %v", n.TTL())
	}
	if n.Message.Topic != "" || n.Message.Message != "" || n.Message.Title != "" {
		t.Errorf("the relay invented content: %+v", n.Message)
	}

	// The handle chooses the push service; the defaults fill what is left out.
	if resp, _ := e.do("POST", "/v1/wake", wakeBody(apnsHandle, "sub-2", "msg-2")); resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	a := e.apns.all()
	if len(a) != 1 || a[0].Device.Target != apnsToken || a[0].Message.Priority != 3 || a[0].TTL() != 24*time.Hour {
		t.Errorf("apns got %+v", a)
	}

	// A year is not honoured.
	e.do("POST", "/v1/wake", `{"handle":"`+apnsHandle+`","sub":"s","id":"m","ttl":31536000}`)
	if got := e.apns.all()[1].TTL(); got != push.MaxWakeTTL {
		t.Errorf("TTL = %v, want it capped at four weeks", got)
	}
}

func TestAWakeRequestIsChecked(t *testing.T) {
	e := newEnv(t)
	handle, _ := e.register(fcmBody(fcmToken))
	tests := []struct {
		name    string
		body    string
		code    int
		mention string
	}{
		{"not JSON", `handle=1`, apierr.InvalidBody, "invalid JSON"},
		{"no body", ``, apierr.InvalidBody, "JSON body"},
		{"no handle", `{"sub":"s","id":"m"}`, apierr.InvalidParameter, "handle"},
		{"a malformed handle", `{"handle":"short","sub":"s","id":"m"}`, apierr.InvalidParameter, "handle"},
		{"no sub", `{"handle":"` + handle + `","id":"m"}`, apierr.InvalidParameter, "sub"},
		{"a sub with odd characters", `{"handle":"` + handle + `","sub":"a b","id":"m"}`, apierr.InvalidParameter, "sub"},
		{"no id", `{"handle":"` + handle + `","sub":"s"}`, apierr.InvalidParameter, "id"},
		{"a priority out of range", `{"handle":"` + handle + `","sub":"s","id":"m","priority":9}`, apierr.InvalidParameter, "priority"},
		{"a negative ttl", `{"handle":"` + handle + `","sub":"s","id":"m","ttl":-5}`, apierr.InvalidParameter, "ttl"},
		{"a handle nobody has", wakeBody(strings.Repeat("a", 32), "s", "m"), apierr.NotFound, "no such handle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, data := e.do("POST", "/v1/wake", tt.body)
			wantError(t, resp, data, tt.code, tt.mention)
		})
	}
	if n := len(e.fcm.all()); n != 0 {
		t.Errorf("%d notifications sent for refused requests", n)
	}

	// A newer server may say more than this relay knows.
	resp, data := e.do("POST", "/v1/wake", `{"handle":"`+handle+`","sub":"s","id":"m","something_new":true}`)
	if resp.StatusCode != 204 {
		t.Errorf("an unknown field in a wake request: %d %s", resp.StatusCode, data)
	}
}

func TestWhatThePushServiceSaysIsPassedOn(t *testing.T) {
	e := newEnv(t)
	handle, _ := e.register(fcmBody(fcmToken))

	e.fcm.fn = func(push.Notification) error { return push.Transient(errors.New("FCM answered 429"), 90*time.Second) }
	resp, data := e.do("POST", "/v1/wake", wakeBody(handle, "s", "m"))
	wantError(t, resp, data, apierr.Unavailable, "try again")
	if resp.Header.Get("Retry-After") != "90" {
		t.Errorf("Retry-After = %q: the server needs it to know when to retry", resp.Header.Get("Retry-After"))
	}

	e.fcm.fn = func(push.Notification) error { return errors.New("connection reset") }
	resp, data = e.do("POST", "/v1/wake", wakeBody(handle, "s", "m"))
	wantError(t, resp, data, apierr.Unavailable, "try again")
	if resp.Header.Get("Retry-After") != "" {
		t.Errorf("Retry-After = %q, want none: the service said nothing", resp.Header.Get("Retry-After"))
	}

	e.fcm.fn = func(push.Notification) error { return push.Permanent(errors.New("payload too large")) }
	resp, data = e.do("POST", "/v1/wake", wakeBody(handle, "s", "m"))
	wantError(t, resp, data, apierr.Undeliverable, "refused")
	if _, err := e.store.Find(bg, push.HashSecret(handle)); err != nil {
		t.Errorf("a refusal removed the handle: %v", err)
	}

	// The device is gone: the handle goes, and the server is told to forget it.
	e.fcm.fn = func(push.Notification) error { return push.Gone(errors.New("UNREGISTERED")) }
	resp, data = e.do("POST", "/v1/wake", wakeBody(handle, "s", "m"))
	wantError(t, resp, data, apierr.NotFound, "no longer be reached")
	if _, err := e.store.Find(bg, push.HashSecret(handle)); !errors.Is(err, ErrNotFound) {
		t.Errorf("the handle of a dead device survived: %v", err)
	}
}

func TestACredentialRemovedAfterRegistrationMakesThatAppUndeliverable(t *testing.T) {
	e := newEnv(t)
	handle, _ := e.register(fcmBody(fcmToken))
	e.relay.Senders = push.NewRegistry() // the operator dropped every app
	resp, data := e.do("POST", "/v1/wake", wakeBody(handle, "s", "m"))
	wantError(t, resp, data, apierr.Undeliverable, "no longer wake")
	if _, err := e.store.Find(bg, push.HashSecret(handle)); err != nil {
		t.Errorf("the handle was removed: %v", err)
	}
}

func TestAHandleCannotBeWokenFasterThanTheLimit(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.WakesPerHandle = 30 }) // a burst of 7, then one every two seconds
	busy, _ := e.register(fcmBody(fcmToken))
	calm, _ := e.register(fcmBody(fcmToken + "2"))

	allowed := 0
	var last *http.Response
	var lastData []byte
	for i := 0; i < 20; i++ {
		resp, data := e.do("POST", "/v1/wake", wakeBody(busy, "s", "m"))
		if resp.StatusCode == 204 {
			allowed++
		} else {
			last, lastData = resp, data
		}
	}
	if allowed != 7 {
		t.Errorf("%d wake-ups allowed in a burst, want 7", allowed)
	}
	wantError(t, last, lastData, apierr.RateLimited, "wake-ups for this device")
	if last.Header.Get("Retry-After") != "2" { // 30 a minute is one every two seconds
		t.Errorf("Retry-After = %q", last.Header.Get("Retry-After"))
	}
	// Another device is unaffected.
	if resp, _ := e.do("POST", "/v1/wake", wakeBody(calm, "s", "m")); resp.StatusCode != 204 {
		t.Errorf("another handle: %d", resp.StatusCode)
	}
	// And time heals.
	e.clock.advance(3 * time.Second)
	if resp, _ := e.do("POST", "/v1/wake", wakeBody(busy, "s", "m")); resp.StatusCode != 204 {
		t.Errorf("after waiting: %d", resp.StatusCode)
	}
	if n := len(e.fcm.all()); n != 7+1+1 {
		t.Errorf("%d pushes, want only the allowed ones", n)
	}
}

func TestOneAddressCannotFloodTheRelay(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.WakesPerIP = 20; c.RegistrationsPerIP = 8 })
	handle, _ := e.register(fcmBody(fcmToken))

	limited := func(method, path, body string) (allowed int) {
		for i := 0; i < 40; i++ {
			resp, _ := e.do(method, path, body)
			if resp.StatusCode != 429 {
				allowed++
			}
		}
		return allowed
	}
	if got := limited("POST", "/v1/wake", wakeBody(strings.Repeat("a", 32), "s", "m")); got != 5 {
		t.Errorf("%d wake requests from one address allowed, want a burst of 5", got)
	}
	// Registrations count separately (and the one above counts already).
	if got := limited("POST", "/v1/handles", fcmBody(fcmToken+"x")); got != 4 {
		t.Errorf("%d registrations from one address allowed, want 4 more (a burst of 5, one used above)", got)
	}
	_ = handle
}

func TestForwardedAddressesAreBelievedOnlyFromTrustedProxies(t *testing.T) {
	post := func(e *env, ip string) int {
		resp, _ := e.do("POST", "/v1/wake", wakeBody(strings.Repeat("a", 32), "s", "m"), "X-Forwarded-For", ip)
		return resp.StatusCode
	}
	exhaust := func(e *env, ip string) {
		for i := 0; i < 20; i++ {
			post(e, ip)
		}
	}

	// Behind a trusted proxy, clients are told apart.
	trusted, _ := auth.ParseProxies([]string{"127.0.0.1"})
	e := newEnv(t, func(c *Config) { c.WakesPerIP = 20; c.Proxies = trusted })
	exhaust(e, "198.51.100.1")
	if got := post(e, "198.51.100.1"); got != 429 {
		t.Errorf("the flooding client: %d, want 429", got)
	}
	if got := post(e, "198.51.100.2"); got != 404 {
		t.Errorf("another client behind the same proxy: %d, want it served (404: no such handle)", got)
	}

	// With no proxy trusted, the header is only a claim, and everyone is the one
	// connecting: a client can't escape its limit by lying about who it is.
	e2 := newEnv(t, func(c *Config) { c.WakesPerIP = 20 })
	exhaust(e2, "198.51.100.1")
	if got := post(e2, "198.51.100.99"); got != 429 {
		t.Errorf("a client that names a new address: %d, want 429", got)
	}
}

func TestNothingIdentifyingIsLogged(t *testing.T) {
	e := newEnv(t)
	handle, secret := e.register(fcmBody(fcmToken))
	e.do("POST", "/v1/wake", wakeBody(handle, "subscription-abc", "message-xyz"))
	e.fcm.fn = func(push.Notification) error { return push.Transient(errors.New("down"), 0) }
	e.do("POST", "/v1/wake", wakeBody(handle, "subscription-abc", "message-xyz"))
	e.do("DELETE", "/v1/handles/"+handle, "", HandleSecretHeader, secret)

	logs := e.logs.String()
	if !strings.Contains(logs, "POST /v1/wake") {
		t.Fatalf("nothing was logged:\n%s", logs)
	}
	for _, leak := range []string{handle, secret, fcmToken, "subscription-abc", "message-xyz"} {
		if strings.Contains(logs, leak) {
			t.Errorf("the log contains %q:\n%s", leak, logs)
		}
	}
}

func TestTheRelayDescribesItself(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do("GET", "/v1/config", "")
	var c struct {
		Role string
		Apps map[string][]string
	}
	json.Unmarshal(data, &c)
	if resp.StatusCode != 200 || c.Role != "push-relay" || strings.Join(c.Apps["fcm"], ",") != "org.example.flare" || strings.Join(c.Apps["apns"], ",") != "org.example.flare" {
		t.Errorf("%d %s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), "secret") {
		t.Errorf("the config mentions a secret: %s", data)
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		if resp, _ := e.do("GET", path, ""); resp.StatusCode != 200 {
			t.Errorf("%s: %d", path, resp.StatusCode)
		}
	}
}

func TestTheRelaysDatabaseIsItsOwn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.db")
	// A relay opened on a file that a server already uses must not disturb it.
	st, err := OpenStore(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	st.Register(bg, Handle{Hash: "h", SecretHash: "s", AppID: "a.b", Platform: "ios", Provider: push.APNs, Token: apnsToken, CreatedAt: time.Unix(1, 0)})
	st.Close()

	again, err := OpenStore(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if n, _ := again.Count(bg); n != 1 {
		t.Errorf("%d handles after reopening", n)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// api/relay.yaml is the contract; a test fails if the routes and the file disagree.
func TestRoutesMatchTheRelaySpec(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "relay.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	var inSpec []string
	for path, ops := range spec.Paths {
		for method := range ops {
			switch method {
			case "get", "post", "put", "delete", "patch":
				inSpec = append(inSpec, strings.ToUpper(method)+" "+path)
			}
		}
	}
	var inCode []string
	for _, rt := range New(Config{Senders: push.NewRegistry()}).Routes() {
		inCode = append(inCode, rt)
	}
	if strings.Join(sorted(inSpec), "\n") != strings.Join(sorted(inCode), "\n") {
		t.Errorf("the relay and api/relay.yaml disagree.\nspec: %v\ncode: %v", sorted(inSpec), sorted(inCode))
	}
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
