package api

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/push"
)

type stubSender struct{}

func (stubSender) MaxPayload() int                               { return 4000 }
func (stubSender) Send(context.Context, push.Notification) error { return nil }

type keyedSender struct{ stubSender }

func (keyedSender) PublicKey() string { return "BPublicKeyForBrowsers" }

// withPush turns push notifications on, for FCM (one app), Web Push and
// UnifiedPush.
func (e *env) withPush(edit ...func(*push.ServiceConfig)) *env {
	e.t.Helper()
	r := push.NewRegistry()
	r.AddApp(push.FCM, "org.example.flare", stubSender{})
	r.Add(push.WebPush, keyedSender{})
	r.Add(push.UnifiedPush, stubSender{})
	cfg := push.ServiceConfig{}
	for _, f := range edit {
		f(&cfg)
	}
	e.api.Push = push.NewService(e.store, e.auth, r, cfg)
	return e
}

const fcmToken = "fcm-registration-token-0123456789abcdef"

func fcmDevice(token string) string {
	return fmt.Sprintf(`{"app_id":"org.example.flare","platform":"android","provider":"fcm","token":%q}`, token)
}

// register registers a device and returns its ID and secret.
func (e *env) registerDevice(body string, headers ...string) (id, secret string) {
	e.t.Helper()
	resp, data := e.do("POST", "/v1/devices", body, headers...)
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("register: %d %s", resp.StatusCode, data)
	}
	var out struct{ ID, Secret string }
	decodeInto(e.t, data, &out)
	return out.ID, out.Secret
}

func withSecret(secret string) []string { return []string{"Device-Secret", secret} }

type errBody struct {
	Code  int    `json:"code"`
	Error string `json:"error"`
}

func wantError(t *testing.T, resp *http.Response, data []byte, status, code int, mention string) {
	t.Helper()
	var body errBody
	json.Unmarshal(data, &body)
	if resp.StatusCode != status || body.Code != code || !strings.Contains(body.Error, mention) {
		t.Errorf("got %d %s, want %d (code %d) mentioning %q", resp.StatusCode, data, status, code, mention)
	}
}

func TestDevicesCanBeRegisteredAndFollowTopics(t *testing.T) {
	e := newEnv(t).withPush()
	id, secret := e.registerDevice(fcmDevice(fcmToken))
	if id == "" || !strings.HasPrefix(secret, push.SecretPrefix) {
		t.Fatalf("id %q, secret %q", id, secret)
	}

	resp, data := e.do("PUT", "/v1/devices/"+id+"/subscriptions", `{"topics":["alerts","status"]}`, withSecret(secret)...)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	var subs struct {
		Subscriptions []struct{ Topic, ID string }
	}
	decodeInto(t, data, &subs)
	if len(subs.Subscriptions) != 2 || subs.Subscriptions[0].Topic != "alerts" || subs.Subscriptions[1].Topic != "status" || subs.Subscriptions[0].ID == "" {
		t.Errorf("subscriptions = %+v", subs)
	}

	// A message published now is queued for the device, in the same step.
	e.published("alerts", "disk full")
	jobs, _ := e.store.ClaimJobs(bg, e.hub.Now(), 0, 10)
	if len(jobs) != 1 || jobs[0].Device.ID != id || jobs[0].SubscriptionID != subs.Subscriptions[0].ID {
		t.Errorf("jobs = %+v", jobs)
	}

	resp, _ = e.do("DELETE", "/v1/devices/"+id, "", withSecret(secret)...)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	resp, data = e.do("PUT", "/v1/devices/"+id+"/subscriptions", `{"topics":[]}`, withSecret(secret)...)
	wantError(t, resp, data, 404, apierr.NotFound, "no such device")
}

func TestPushEndpointsSayWhenTheServerHasNoPush(t *testing.T) {
	e := newEnv(t) // without withPush
	for _, req := range []struct{ method, path, body string }{
		{"POST", "/v1/devices", fcmDevice(fcmToken)},
		{"PUT", "/v1/devices/x/subscriptions", `{"topics":[]}`},
		{"DELETE", "/v1/devices/x", ""},
	} {
		resp, data := e.do(req.method, req.path, req.body, withSecret("flrd_x")...)
		wantError(t, resp, data, 404, apierr.NotFound, "does not send push")
	}
}

func TestRegistrationIsRefusedWithTheReason(t *testing.T) {
	e := newEnv(t).withPush()
	tests := []struct {
		name   string
		body   string
		status int
		code   int
		reason string
	}{
		{"a field that does not exist", `{"app_id":"org.example.flare","platform":"android","provider":"fcm","token":"` + fcmToken + `","colour":"red"}`, 400, apierr.InvalidBody, "colour"},
		{"not JSON", `token=abc`, 400, apierr.InvalidBody, "invalid JSON"},
		{"no body", ``, 400, apierr.InvalidBody, "JSON body"},
		{"two objects", fcmDevice(fcmToken) + fcmDevice(fcmToken), 400, apierr.InvalidBody, "unexpected data"},
		{"a bad token", fcmDevice("short"), 400, apierr.InvalidParameter, "token"},
		{"a bad provider", `{"app_id":"org.example.flare","platform":"android","provider":"smoke","token":"` + fcmToken + `"}`, 400, apierr.InvalidParameter, "provider"},
		{"an app the server has no credentials for", `{"app_id":"com.other.app","platform":"android","provider":"fcm","token":"` + fcmToken + `"}`, 400, apierr.Unsupported, "com.other.app"},
		{"apns, which is not set up", `{"app_id":"org.example.flare","platform":"ios","provider":"apns","token":"` + strings.Repeat("ab", 32) + `"}`, 400, apierr.Unsupported, "APNS"},
		{"an endpoint inside the network", `{"app_id":"a","platform":"android","provider":"unifiedpush","endpoint":"https://169.254.169.254/latest/meta-data"}`, 400, apierr.InvalidParameter, "public"},
		{"too large", `{"app_id":"` + strings.Repeat("a", 20000) + `"}`, 413, apierr.TooLarge, "16 KiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, data := e.do("POST", "/v1/devices", tt.body)
			wantError(t, resp, data, tt.status, tt.code, tt.reason)
		})
	}
}

func TestARelayDeviceIsRegisteredWithItsRelayAndHandle(t *testing.T) {
	e := newEnv(t).withPush()
	e.api.Push = push.NewService(e.store, e.auth, func() *push.Registry {
		r := push.NewRegistry()
		r.AddRelay("https://push.example.org", stubSender{})
		return r
	}(), push.ServiceConfig{})

	body := func(relay, handle string) string {
		return fmt.Sprintf(`{"app_id":"org.example.flare","platform":"ios","provider":"relay","relay":%q,"handle":%q}`, relay, handle)
	}
	id, secret := e.registerDevice(body("https://push.example.org/", "relay-handle-0123456789abcdef"))
	d, err := e.store.Device(bg, id)
	if err != nil || d.Provider != push.Relay || d.RelayURL != "https://push.example.org" || d.Target != "relay-handle-0123456789abcdef" {
		t.Errorf("stored %+v, %v", d, err)
	}
	if resp, data := e.do("PUT", "/v1/devices/"+id+"/subscriptions", `{"topics":["news"]}`, withSecret(secret)...); resp.StatusCode != 200 {
		t.Errorf("following: %d %s", resp.StatusCode, data)
	}

	resp, data := e.do("POST", "/v1/devices", body("https://evil.example.net", "relay-handle-0123456789abcdef"))
	wantError(t, resp, data, 400, apierr.Unsupported, "that relay")
	resp, data = e.do("POST", "/v1/devices", body("https://push.example.org", "bad"))
	wantError(t, resp, data, 400, apierr.InvalidParameter, "handle")
}

func TestWebPushRegistrationNeedsAGenuineBrowserKey(t *testing.T) {
	e := newEnv(t).withPush()
	priv, _ := ecdh.P256().GenerateKey(rand.Reader)
	authSecret := make([]byte, 16)
	rand.Read(authSecret)
	enc := base64.RawURLEncoding.EncodeToString
	body := func(p256dh string) string {
		return fmt.Sprintf(`{"app_id":"web","platform":"web","provider":"webpush","endpoint":"https://push.example.com/x","keys":{"p256dh":%q,"auth":%q}}`, p256dh, enc(authSecret))
	}
	if resp, data := e.do("POST", "/v1/devices", body(enc(priv.PublicKey().Bytes()))); resp.StatusCode != 201 {
		t.Errorf("a real key: %d %s", resp.StatusCode, data)
	}
	resp, data := e.do("POST", "/v1/devices", body(enc(make([]byte, 65))))
	wantError(t, resp, data, 400, apierr.InvalidParameter, "keys.p256dh")
}

func TestOnlyTheDeviceItselfCanManageIt(t *testing.T) {
	e := newEnv(t).withPush()
	id, secret := e.registerDevice(fcmDevice(fcmToken))
	_, otherSecret := e.registerDevice(fcmDevice(fcmToken + "-other"))
	path := "/v1/devices/" + id + "/subscriptions"

	resp, data := e.do("PUT", path, `{"topics":[]}`)
	wantError(t, resp, data, 401, apierr.Unauthorized, "Device-Secret")
	if resp.Header.Get("WWW-Authenticate") != "" {
		t.Error("a Bearer challenge for a request that has no use for a token")
	}
	resp, data = e.do("PUT", path, `{"topics":[]}`, withSecret(otherSecret)...)
	wantError(t, resp, data, 401, apierr.InvalidCredentials, "not this device's")
	resp, data = e.do("DELETE", "/v1/devices/"+id, "", withSecret(otherSecret)...)
	wantError(t, resp, data, 401, apierr.InvalidCredentials, "not this device's")
	// A user's own token is not a device secret.
	resp, data = e.do("PUT", path, `{"topics":[]}`, bearer("flr_"+strings.Repeat("x", 43))...)
	wantError(t, resp, data, 401, apierr.Unauthorized, "Device-Secret")

	if resp, _ := e.do("PUT", path, `{"topics":[]}`, withSecret(secret)...); resp.StatusCode != 200 {
		t.Errorf("the device's own secret: %d", resp.StatusCode)
	}
}

func TestADeviceFollowsWhatItsOwnerMayRead(t *testing.T) {
	e := newEnv(t, denyAll()).withPush()
	e.addUser("alice", auth.RoleUser)
	e.grant("alice", "alerts-*", auth.ReadOnly)
	token, _ := e.tokenFor("alice")

	id, secret := e.registerDevice(fcmDevice(fcmToken), bearer(token)...)
	path := "/v1/devices/" + id + "/subscriptions"

	if resp, data := e.do("PUT", path, `{"topics":["alerts-db"]}`, withSecret(secret)...); resp.StatusCode != 200 {
		t.Fatalf("a topic her rule covers: %d %s", resp.StatusCode, data)
	}
	resp, data := e.do("PUT", path, `{"topics":["alerts-db","payroll"]}`, withSecret(secret)...)
	wantError(t, resp, data, 403, apierr.Forbidden, `"payroll"`)

	// The refused update changed nothing: the device still follows alerts-db.
	e.addUser("root", auth.RoleAdmin)
	rootToken, _ := e.tokenFor("root")
	e.published("alerts-db", "x", bearer(rootToken)...)
	if jobs, _ := e.store.ClaimJobs(bg, e.hub.Now(), 0, 10); len(jobs) != 1 || jobs[0].Topic != "alerts-db" {
		t.Errorf("jobs = %+v, want one for alerts-db", jobs)
	}
}

func TestAnonymousRegistrationFollowsTheServersPolicy(t *testing.T) {
	// On a server that lets nobody in, a device with no owner could follow nothing.
	closed := newEnv(t, denyAll()).withPush()
	resp, data := closed.do("POST", "/v1/devices", fcmDevice(fcmToken))
	wantError(t, resp, data, 401, apierr.Unauthorized, "log in to register")
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("a 401 without a challenge")
	}

	open := newEnv(t, access(auth.ReadOnly)).withPush()
	id, secret := open.registerDevice(fcmDevice(fcmToken))
	if resp, data := open.do("PUT", "/v1/devices/"+id+"/subscriptions", `{"topics":["news"]}`, withSecret(secret)...); resp.StatusCode != 200 {
		t.Errorf("an anonymous device on an open server: %d %s", resp.StatusCode, data)
	}

	// Wrong credentials are not downgraded to anonymous.
	resp, data = closed.do("POST", "/v1/devices", fcmDevice(fcmToken+"2"), bearer("flr_"+strings.Repeat("y", 43))...)
	wantError(t, resp, data, 401, apierr.InvalidCredentials, "invalid credentials")
}

func TestTheDeviceLimitIsReported(t *testing.T) {
	e := newEnv(t).withPush(func(c *push.ServiceConfig) { c.MaxAnonymousDevices = 2 })
	e.registerDevice(fcmDevice(fcmToken + "1"))
	e.registerDevice(fcmDevice(fcmToken + "2"))
	resp, data := e.do("POST", "/v1/devices", fcmDevice(fcmToken+"3"))
	wantError(t, resp, data, 429, apierr.TooManyDevices, "too many devices")
}

func TestFetchingOneMessage(t *testing.T) {
	e := newEnv(t, denyAll())
	e.addUser("alice", auth.RoleUser)
	e.addUser("bob", auth.RoleUser)
	e.grant("alice", "alerts", auth.ReadOnly)
	e.grant("bob", "other", auth.ReadWrite)
	alice, _ := e.tokenFor("alice")
	bob, _ := e.tokenFor("bob")
	e.addUser("root", auth.RoleAdmin)
	rootToken, _ := e.tokenFor("root")
	m := e.published("alerts", "disk full", append(bearer(rootToken), "Title", "Alert", "Priority", "5")...)

	resp, data := e.do("GET", "/v1/messages/"+m.ID, "", bearer(alice)...)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if got := decodeMsg(t, data); got.ID != m.ID || got.Message != "disk full" || got.Title != "Alert" || got.Priority != 5 || got.Topic != "alerts" {
		t.Errorf("got %+v", got)
	}

	resp, data = e.do("GET", "/v1/messages/"+m.ID, "", bearer(bob)...)
	wantError(t, resp, data, 403, apierr.Forbidden, "may not read")
	resp, data = e.do("GET", "/v1/messages/"+m.ID, "")
	wantError(t, resp, data, 401, apierr.Unauthorized, "log in")
	resp, data = e.do("GET", "/v1/messages/no-such-message", "", bearer(alice)...)
	wantError(t, resp, data, 404, apierr.NotFound, "no such message")
	// A refusal never carries the content.
	if strings.Contains(string(data), "disk full") {
		t.Error("an error leaked content")
	}
	resp, data = e.do("GET", "/v1/messages/"+m.ID, "", bearer("flr_"+strings.Repeat("z", 43))...)
	wantError(t, resp, data, 401, apierr.InvalidCredentials, "invalid credentials")
}

func TestConfigAnnouncesPush(t *testing.T) {
	type cfgBody struct {
		Features []string `json:"features"`
		Push     *struct {
			Providers  []string            `json:"providers"`
			Apps       map[string][]string `json:"apps"`
			Relays     []string            `json:"relays"`
			WebPushKey string              `json:"web_push_public_key"`
		} `json:"push"`
	}
	without := newEnv(t)
	_, data := without.do("GET", "/v1/config", "")
	var got cfgBody
	decodeInto(t, data, &got)
	if got.Push != nil {
		t.Errorf("a server without push announced %+v", got.Push)
	}
	for _, f := range got.Features {
		if f == "push" {
			t.Error("a server without push lists the feature")
		}
	}

	with := newEnv(t).withPush()
	_, data = with.do("GET", "/v1/config", "")
	got = cfgBody{}
	decodeInto(t, data, &got)
	if got.Push == nil || strings.Join(got.Push.Providers, ",") != "unifiedpush,webpush" ||
		strings.Join(got.Push.Apps["fcm"], ",") != "org.example.flare" || got.Push.WebPushKey != "BPublicKeyForBrowsers" {
		t.Errorf("push = %+v", got.Push)
	}
	if !contains(got.Features, "push") {
		t.Errorf("features = %v", got.Features)
	}
	// Nothing secret is announced: no tokens, no credentials, no endpoints.
	if strings.Contains(string(data), "flrd_") || strings.Contains(strings.ToLower(string(data)), "secret") {
		t.Errorf("the config mentions a secret: %s", data)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

var _ = config.Default
