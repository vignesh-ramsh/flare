package push_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/push"
	"github.com/vignesh-ramsh/flare/server/internal/store"
)

var ctx = context.Background()

// fakeAccess lets tests say who may read what.
type fakeAccess struct {
	mu    sync.Mutex
	allow map[string]bool // "user|topic"
	err   error
}

func (a *fakeAccess) MayRead(_ context.Context, userID, topic string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allow[userID+"|"+topic], a.err
}

func (a *fakeAccess) set(userID, topic string, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.allow == nil {
		a.allow = map[string]bool{}
	}
	a.allow[userID+"|"+topic] = ok
}

type nullSender struct{}

func (nullSender) MaxPayload() int                               { return 4000 }
func (nullSender) Send(context.Context, push.Notification) error { return nil }

func newStore(t *testing.T) *store.SQLite {
	t.Helper()
	st, err := store.OpenSQLite(ctx, filepath.Join(t.TempDir(), "flare.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// fullRegistry can reach every kind of device used below.
func fullRegistry() *push.Registry {
	r := push.NewRegistry()
	r.AddApp(push.FCM, "org.example.flare", nullSender{})
	r.AddApp(push.APNs, "org.example.flare", nullSender{})
	r.Add(push.WebPush, nullSender{})
	r.Add(push.UnifiedPush, nullSender{})
	return r
}

type env struct {
	svc    *push.Service
	store  *store.SQLite
	access *fakeAccess
	now    time.Time
}

func newEnv(t *testing.T, edit ...func(*push.ServiceConfig)) *env {
	t.Helper()
	e := &env{store: newStore(t), access: &fakeAccess{}, now: time.Unix(1_800_000_000, 0)}
	cfg := push.ServiceConfig{Now: func() time.Time { return e.now }}
	for _, f := range edit {
		f(&cfg)
	}
	e.svc = push.NewService(e.store, e.access, fullRegistry(), cfg)
	return e
}

// addUser creates a user so that devices can belong to them.
func (e *env) addUser(t *testing.T, id string) {
	t.Helper()
	u := auth.User{ID: id, Name: "user-" + id, Role: auth.RoleUser, CreatedAt: e.now}
	if err := e.store.CreateUser(ctx, u, "hash"); err != nil {
		t.Fatal(err)
	}
}

// register registers a device and fails the test if it is refused.
func (e *env) register(t *testing.T, userID string, r push.Registration) (push.Device, string) {
	t.Helper()
	d, secret, err := e.svc.Register(ctx, userID, r)
	if err != nil {
		t.Fatalf("registering %s: %v", r.Provider, err)
	}
	return d, secret
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// browserKeys makes the keys a browser would hand over with a subscription.
func browserKeys(t *testing.T) push.Keys {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)
	return push.Keys{P256DH: b64(priv.PublicKey().Bytes()), Auth: b64(auth)}
}

const fcmToken = "fcm-registration-token-0123456789abcdef"

func fcm(token string) push.Registration {
	return push.Registration{AppID: "org.example.flare", Platform: "android", Provider: push.FCM, Token: token}
}

func TestRegisteringEachKindOfDevice(t *testing.T) {
	e := newEnv(t)
	keys := browserKeys(t)
	tests := map[string]struct {
		reg    push.Registration
		target string
		keys   push.Keys
	}{
		"fcm":         {fcm(fcmToken), fcmToken, push.Keys{}},
		"apns":        {push.Registration{AppID: "org.example.flare", Platform: "ios", Provider: push.APNs, Token: "AABBCCDDEEFF00112233445566778899AABBCCDDEEFF00112233445566778899"}, "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899", push.Keys{}},
		"webpush":     {push.Registration{AppID: "org.example.web", Platform: "web", Provider: push.WebPush, Endpoint: "https://push.example.com/wp/abc", Keys: keys}, "https://push.example.com/wp/abc", keys},
		"unifiedpush": {push.Registration{AppID: "org.example.flare", Platform: "android", Provider: push.UnifiedPush, Endpoint: "https://up.example.com/abc"}, "https://up.example.com/abc", push.Keys{}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tt.reg.AppVersion = "1.0.0"
			d, secret, err := e.svc.Register(ctx, "", tt.reg)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(secret, push.SecretPrefix) || len(secret) != len(push.SecretPrefix)+43 {
				t.Errorf("secret = %q", secret)
			}
			stored, err := e.store.Device(ctx, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Target != tt.target || stored.Keys != tt.keys || stored.Provider != tt.reg.Provider ||
				stored.AppID != tt.reg.AppID || stored.AppVersion != "1.0.0" || !stored.CreatedAt.Equal(e.now) {
				t.Errorf("stored = %+v", stored)
			}
			if stored.SecretHash != push.HashSecret(secret) || strings.Contains(stored.SecretHash, secret) {
				t.Error("the secret is not kept as a hash")
			}
		})
	}
}

func TestEachRegistrationGetsItsOwnIDAndSecret(t *testing.T) {
	e := newEnv(t)
	a, sa := e.register(t, "", fcm(fcmToken+"1"))
	b, sb := e.register(t, "", fcm(fcmToken+"2"))
	if a.ID == b.ID || sa == sb || a.ID == "" {
		t.Errorf("IDs %q %q, secrets differ: %v", a.ID, b.ID, sa != sb)
	}
	if len(a.ID) < 20 {
		t.Errorf("ID %q is short enough to guess", a.ID)
	}
}

func TestRegistrationIsRefusedWithAReasonThatNamesTheField(t *testing.T) {
	e := newEnv(t)
	keys := browserKeys(t)
	wrongKey := push.Keys{P256DH: b64(make([]byte, 65)), Auth: keys.Auth} // not a point on the curve
	tests := []struct {
		name  string
		reg   push.Registration
		field string
	}{
		{"no app id", push.Registration{Platform: "android", Provider: push.FCM, Token: fcmToken}, "app_id"},
		{"app id with spaces", push.Registration{AppID: "my app", Platform: "android", Provider: push.FCM, Token: fcmToken}, "app_id"},
		{"app id too long", push.Registration{AppID: strings.Repeat("a", 129), Platform: "android", Provider: push.FCM, Token: fcmToken}, "app_id"},
		{"unknown platform", push.Registration{AppID: "org.example.flare", Platform: "beos", Provider: push.FCM, Token: fcmToken}, "platform"},
		{"unknown provider", push.Registration{AppID: "org.example.flare", Platform: "android", Provider: "carrier-pigeon", Token: fcmToken}, "provider"},
		{"no provider", push.Registration{AppID: "org.example.flare", Platform: "android", Token: fcmToken}, "provider"},
		{"version too long", func() push.Registration { r := fcm(fcmToken); r.AppVersion = strings.Repeat("1", 65); return r }(), "app_version"},
		{"fcm token too short", fcm("short"), "token"},
		{"fcm token with a space", fcm("fcm token with spaces 0123456789"), "token"},
		{"fcm token with a newline", fcm("fcm-token-0123456789\nabcdef"), "token"},
		{"fcm with an endpoint", func() push.Registration { r := fcm(fcmToken); r.Endpoint = "https://x.example/"; return r }(), "endpoint"},
		{"apns token not hex", push.Registration{AppID: "org.example.flare", Platform: "ios", Provider: push.APNs, Token: strings.Repeat("zz", 32)}, "token"},
		{"apns token too short", push.Registration{AppID: "org.example.flare", Platform: "ios", Provider: push.APNs, Token: "abcd"}, "token"},
		{"webpush without an endpoint", push.Registration{AppID: "w", Platform: "web", Provider: push.WebPush, Keys: keys}, "endpoint"},
		{"webpush over http", push.Registration{AppID: "w", Platform: "web", Provider: push.WebPush, Endpoint: "http://push.example.com/x", Keys: keys}, "endpoint"},
		{"webpush to localhost", push.Registration{AppID: "w", Platform: "web", Provider: push.WebPush, Endpoint: "https://127.0.0.1/x", Keys: keys}, "endpoint"},
		{"webpush to the metadata service", push.Registration{AppID: "w", Platform: "web", Provider: push.WebPush, Endpoint: "https://169.254.169.254/latest", Keys: keys}, "endpoint"},
		{"webpush without keys", push.Registration{AppID: "w", Platform: "web", Provider: push.WebPush, Endpoint: "https://push.example.com/x"}, "keys.p256dh"},
		{"webpush with a bad public key", push.Registration{AppID: "w", Platform: "web", Provider: push.WebPush, Endpoint: "https://push.example.com/x", Keys: wrongKey}, "keys.p256dh"},
		{"webpush with a short auth secret", push.Registration{AppID: "w", Platform: "web", Provider: push.WebPush, Endpoint: "https://push.example.com/x", Keys: push.Keys{P256DH: keys.P256DH, Auth: b64([]byte("short"))}}, "keys.auth"},
		{"webpush with a token", push.Registration{AppID: "w", Platform: "web", Provider: push.WebPush, Endpoint: "https://push.example.com/x", Keys: keys, Token: "x"}, "token"},
		{"unifiedpush with keys", push.Registration{AppID: "w", Platform: "android", Provider: push.UnifiedPush, Endpoint: "https://up.example.com/x", Keys: keys}, "provider"},
		{"unifiedpush to a private address", push.Registration{AppID: "w", Platform: "android", Provider: push.UnifiedPush, Endpoint: "https://10.0.0.5/x"}, "endpoint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := e.svc.Register(ctx, "", tt.reg)
			var in *push.InputError
			if !errors.As(err, &in) || in.Field != tt.field {
				t.Errorf("err = %v, want an InputError about %q", err, tt.field)
			}
		})
	}
}

func TestTheSameWebPushKeysAreAcceptedWithPaddingAndStoredWithout(t *testing.T) {
	e := newEnv(t)
	keys := browserKeys(t)
	padded := push.Keys{P256DH: keys.P256DH + "=", Auth: keys.Auth + "=="}
	d, _, err := e.svc.Register(ctx, "", push.Registration{AppID: "w", Platform: "web", Provider: push.WebPush, Endpoint: "https://push.example.com/x", Keys: padded})
	if err != nil {
		t.Fatal(err)
	}
	if d.Keys != keys {
		t.Errorf("keys = %+v, want them normalised to %+v", d.Keys, keys)
	}
}

func TestPrivateEndpointsAreAllowedOnlyWhenTheOperatorSaysSo(t *testing.T) {
	reg := push.Registration{AppID: "w", Platform: "android", Provider: push.UnifiedPush, Endpoint: "http://192.168.1.10:8080/up/abc"}
	if _, _, err := newEnv(t).svc.Register(ctx, "", reg); err == nil {
		t.Error("a private http endpoint was accepted by default")
	}
	e := newEnv(t, func(c *push.ServiceConfig) { c.AllowPrivateEndpoints = true })
	if _, _, err := e.svc.Register(ctx, "", reg); err != nil {
		t.Errorf("with private endpoints allowed: %v", err)
	}
}

func TestADeviceTheServerCannotReachIsRefusedRatherThanSilentlyNeverNotified(t *testing.T) {
	e := newEnv(t)
	// Credentials exist for org.example.flare, not for another app.
	other := fcm(fcmToken)
	other.AppID = "com.someone.else"
	_, _, err := e.svc.Register(ctx, "", other)
	if !errors.Is(err, push.ErrUnsupported) || !strings.Contains(err.Error(), "com.someone.else") {
		t.Errorf("err = %v, want ErrUnsupported naming the app", err)
	}

	// A server with only the providers that need no credentials.
	r := push.NewRegistry()
	r.Add(push.WebPush, nullSender{})
	svc := push.NewService(newStore(t), &fakeAccess{}, r, push.ServiceConfig{})
	if _, _, err := svc.Register(ctx, "", fcm(fcmToken)); !errors.Is(err, push.ErrUnsupported) {
		t.Errorf("fcm without credentials: %v", err)
	}
	up := push.Registration{AppID: "a", Platform: "android", Provider: push.UnifiedPush, Endpoint: "https://up.example.com/x"}
	if _, _, err := svc.Register(ctx, "", up); !errors.Is(err, push.ErrUnsupported) || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("unifiedpush when not enabled: %v", err)
	}
}

func TestRegisteringAgainReplacesTheDeviceAndGivesANewSecret(t *testing.T) {
	e := newEnv(t)
	first, oldSecret := e.register(t, "", fcm(fcmToken))
	e.access.set("", "alerts", true)
	if _, err := e.svc.Follow(ctx, first.ID, oldSecret, []string{"alerts"}); err != nil {
		t.Fatal(err)
	}

	second, newSecret, err := e.svc.Register(ctx, "", fcm(fcmToken))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || newSecret == oldSecret {
		t.Error("the replacement kept the old identity")
	}
	if _, err := e.svc.Follow(ctx, first.ID, oldSecret, nil); !errors.Is(err, push.ErrNotFound) {
		t.Errorf("the replaced device still works: %v", err)
	}
	// And it starts with no subscriptions: the app sets them again.
	subs, err := e.svc.Follow(ctx, second.ID, newSecret, nil)
	if err != nil || len(subs) != 0 {
		t.Errorf("subs = %v, %v", subs, err)
	}
}

func TestDeviceLimits(t *testing.T) {
	e := newEnv(t, func(c *push.ServiceConfig) { c.MaxDevicesPerUser, c.MaxAnonymousDevices = 2, 3 })
	for i := 0; i < 2; i++ {
		if _, _, err := e.svc.Register(ctx, "", fcm(fmt.Sprintf("%s-anon-%d", fcmToken, i))); err != nil {
			t.Fatal(err)
		}
	}
	// Users and anonymous visitors have separate allowances.
	if _, _, err := e.svc.Register(ctx, "", fcm(fcmToken+"-anon-3")); err != nil {
		t.Errorf("third anonymous device: %v", err)
	}
	if _, _, err := e.svc.Register(ctx, "", fcm(fcmToken+"-anon-4")); !errors.Is(err, push.ErrTooManyDevices) {
		t.Errorf("fourth anonymous device: %v", err)
	}
}

func TestFollowingTopics(t *testing.T) {
	e := newEnv(t)
	e.access.set("", "alerts", true)
	e.access.set("", "status", true)
	d, secret := e.register(t, "", fcm(fcmToken))

	subs, err := e.svc.Follow(ctx, d.ID, secret, []string{"alerts", "status"})
	if err != nil || len(subs) != 2 || subs[0].Topic != "alerts" || subs[1].Topic != "status" {
		t.Fatalf("subs = %+v, %v", subs, err)
	}
	if subs[0].ID == "" || subs[0].ID == subs[1].ID {
		t.Errorf("subscription IDs = %q, %q", subs[0].ID, subs[1].ID)
	}
	again, _ := e.svc.Follow(ctx, d.ID, secret, []string{"status", "alerts"})
	ids := map[string]string{subs[0].Topic: subs[0].ID, subs[1].Topic: subs[1].ID}
	for _, s := range again {
		if ids[s.Topic] != s.ID {
			t.Errorf("the ID of %s changed from %s to %s", s.Topic, ids[s.Topic], s.ID)
		}
	}
	if seen, _ := e.store.Device(ctx, d.ID); !seen.SeenAt.Equal(e.now) {
		t.Errorf("SeenAt = %v", seen.SeenAt)
	}
}

func TestFollowingChecksTheOwnersAccessAndChangesNothingIfAnyTopicIsRefused(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "u1")
	e.access.set("u1", "alerts", true)
	e.access.set("u1", "status", true)
	d, secret := e.register(t, "u1", fcm(fcmToken))
	before, err := e.svc.Follow(ctx, d.ID, secret, []string{"alerts"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = e.svc.Follow(ctx, d.ID, secret, []string{"status", "secret", "alerts"})
	var forbidden *push.ForbiddenError
	if !errors.As(err, &forbidden) || forbidden.Topic != "secret" || forbidden.Anonymous {
		t.Fatalf("err = %v, want the topic that was refused", err)
	}
	after, _ := e.svc.Follow(ctx, d.ID, secret, []string{"alerts"})
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a refused update changed the subscriptions: %+v -> %+v", before, after)
	}

	// An anonymous device is told why it is more limited.
	anon, anonSecret := e.register(t, "", fcm(fcmToken+"-anon"))
	_, err = e.svc.Follow(ctx, anon.ID, anonSecret, []string{"alerts"})
	if !errors.As(err, &forbidden) || !forbidden.Anonymous || !strings.Contains(err.Error(), "without logging in") {
		t.Errorf("err = %v", err)
	}

	// A failure to find out is not a refusal: the caller hears it was our problem.
	e.access.err = errors.New("database is locked")
	if _, err := e.svc.Follow(ctx, d.ID, secret, []string{"alerts"}); err == nil || errors.As(err, &forbidden) {
		t.Errorf("err = %v, want the lookup failure", err)
	}
}

func TestFollowingRefusesBadTopicLists(t *testing.T) {
	e := newEnv(t, func(c *push.ServiceConfig) { c.MaxTopicsPerDevice = 3 })
	for _, topic := range []string{"a", "b", "c", "d", "bad topic", "v1", "healthz"} {
		e.access.set("", topic, true)
	}
	d, secret := e.register(t, "", fcm(fcmToken))
	tests := map[string][]string{
		"too many":     {"a", "b", "c", "d"},
		"duplicates":   {"a", "b", "a"},
		"bad name":     {"bad topic"},
		"reserved":     {"v1"},
		"reserved too": {"healthz"},
		"empty name":   {""},
	}
	for name, topics := range tests {
		var in *push.InputError
		if _, err := e.svc.Follow(ctx, d.ID, secret, topics); !errors.As(err, &in) || in.Field != "topics" {
			t.Errorf("%s: err = %v, want an InputError about topics", name, err)
		}
	}
}

func TestOnlyTheDeviceWithTheSecretCanChangeIt(t *testing.T) {
	e := newEnv(t)
	e.access.set("", "a", true)
	d, secret := e.register(t, "", fcm(fcmToken))
	other, otherSecret := e.register(t, "", fcm(fcmToken+"-other"))

	for name, try := range map[string]func() error{
		"a wrong secret":          func() error { _, err := e.svc.Follow(ctx, d.ID, "flrd_wrong", []string{"a"}); return err },
		"no secret":               func() error { _, err := e.svc.Follow(ctx, d.ID, "", []string{"a"}); return err },
		"another device's secret": func() error { _, err := e.svc.Follow(ctx, d.ID, otherSecret, []string{"a"}); return err },
		"remove with a wrong one": func() error { return e.svc.Remove(ctx, d.ID, otherSecret) },
	} {
		if err := try(); !errors.Is(err, push.ErrBadSecret) {
			t.Errorf("%s: err = %v, want ErrBadSecret", name, err)
		}
	}
	if _, err := e.svc.Follow(ctx, "no-such-device", secret, nil); !errors.Is(err, push.ErrNotFound) {
		t.Errorf("unknown device: %v", err)
	}
	if _, err := e.store.Device(ctx, d.ID); err != nil {
		t.Errorf("the device was removed by someone else: %v", err)
	}

	if err := e.svc.Remove(ctx, d.ID, secret); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Device(ctx, d.ID); !errors.Is(err, push.ErrNotFound) {
		t.Errorf("after Remove: %v", err)
	}
	if _, err := e.store.Device(ctx, other.ID); err != nil {
		t.Errorf("another device went too: %v", err)
	}
}

func testLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
