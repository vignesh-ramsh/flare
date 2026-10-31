package settings

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/config"
)

var ctx = context.Background()

type memStore struct {
	mu   sync.Mutex
	data map[string]string
	fail error
}

func newMem() *memStore { return &memStore{data: map[string]string{}} }

func (m *memStore) Settings(_ context.Context, prefix string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for k, v := range m.data {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out, nil
}

func (m *memStore) PutSetting(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.data[k] = v
	return nil
}

func (m *memStore) DeleteSetting(_ context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	delete(m.data, k)
	return nil
}

func base() config.Config {
	c := config.Default()
	c.BaseURL = "https://flare.example.com"
	return c
}

func newManager(t *testing.T, st *memStore) *Manager {
	t.Helper()
	m, err := New(ctx, st, base())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func set(t *testing.T, m *Manager, key, value string) Item {
	t.Helper()
	it, err := m.Set(ctx, key, json.RawMessage(value))
	if err != nil {
		t.Fatalf("Set(%s, %s): %v", key, value, err)
	}
	return it
}

func item(t *testing.T, m *Manager, key string) Item {
	t.Helper()
	for _, it := range m.List() {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no item %s", key)
	return Item{}
}

func TestEverySettingIsDescribed(t *testing.T) {
	groups := map[string]bool{}
	for _, g := range Groups() {
		groups[g.ID] = true
	}
	seen := map[string]bool{}
	for _, d := range Defs() {
		if seen[d.Key] {
			t.Errorf("%s is listed twice", d.Key)
		}
		seen[d.Key] = true
		if d.Label == "" || d.Help == "" || !groups[d.Group] || d.configKey == "" {
			t.Errorf("%s is not fully described: %+v", d.Key, d)
		}
		// Reading it never panics, and what it reads is what decode would accept back.
		v := d.get(base())
		raw, _ := json.Marshal(v)
		if _, err := d.decode(raw); err != nil && !(d.Kind == String && d.Min > 0 && v == "") {
			t.Errorf("%s: its own value %s is refused: %v", d.Key, raw, err)
		}
	}
}

func TestOnlyPushNeedsARestart(t *testing.T) {
	for _, d := range Defs() {
		if want := strings.HasPrefix(d.Key, "push."); d.Restart != want {
			t.Errorf("%s: restart = %v", d.Key, d.Restart)
		}
	}
}

func TestWithNothingChangedTheConfigurationIsTheBase(t *testing.T) {
	m := newManager(t, newMem())
	if !reflect.DeepEqual(m.Config(), base()) {
		t.Errorf("config differs from the base: %+v", m.Config())
	}
	for _, it := range m.List() {
		if it.Overridden || it.PendingRestart || !reflect.DeepEqual(it.Value, it.Default) {
			t.Errorf("%s: %+v", it.Key, it)
		}
	}
	if len(m.Warnings) != 0 {
		t.Errorf("warnings: %v", m.Warnings)
	}
}

func TestChangingEachKindOfSetting(t *testing.T) {
	st := newMem()
	m := newManager(t, st)
	for _, tc := range []struct {
		key, value string
		check      func(config.Config) bool
		stored     string
	}{
		{"branding.name", `"Acme Alerts"`, func(c config.Config) bool { return c.Branding.Name == "Acme Alerts" }, `"Acme Alerts"`},
		{"branding.name", `"  Padded  "`, func(c config.Config) bool { return c.Branding.Name == "Padded" }, `"Padded"`},
		{"branding.primary_color", `"#1D4ED8"`, func(c config.Config) bool { return c.Branding.PrimaryColor == "#1D4ED8" }, `"#1D4ED8"`},
		{"branding.app_url_scheme", `"acme-alerts"`, func(c config.Config) bool { return c.Branding.AppURLScheme == "acme-alerts" }, `"acme-alerts"`},
		{"base_url", `"https://alerts.acme.example/"`, func(c config.Config) bool { return c.BaseURL == "https://alerts.acme.example" }, `"https://alerts.acme.example"`},
		{"auth.default_access", `"deny-all"`, func(c config.Config) bool { return c.Auth.DefaultAccess == "deny-all" }, `"deny-all"`},
		{"message_size_limit", `8192`, func(c config.Config) bool { return c.MessageSizeLimit == 8192 }, `8192`},
		{"cache_duration", `7200`, func(c config.Config) bool { return c.CacheDuration == 2*time.Hour }, `7200`},
		{"push.content_free", `true`, func(c config.Config) bool { return c.Push.ContentFree }, `true`},
		{"push.allowed_relays", `[" https://relay.example ", "", "https://other.example"]`, func(c config.Config) bool {
			return reflect.DeepEqual(c.Push.AllowedRelays, []string{"https://relay.example", "https://other.example"})
		}, `["https://relay.example","https://other.example"]`},
		{"push.vapid_subject", `"mailto:admin@example.com"`, func(c config.Config) bool { return c.Push.VAPIDSubject == "mailto:admin@example.com" }, `"mailto:admin@example.com"`},
	} {
		it := set(t, m, tc.key, tc.value)
		if !tc.check(m.Config()) {
			t.Errorf("%s = %s: config is %+v", tc.key, tc.value, m.Config())
		}
		if got := st.data["admin."+tc.key]; got != tc.stored {
			t.Errorf("%s stored as %q, want %q", tc.key, got, tc.stored)
		}
		if !it.Overridden {
			t.Errorf("%s is not marked overridden", tc.key)
		}
	}
}

func TestOnlyThePushSettingsWaitForARestart(t *testing.T) {
	m := newManager(t, newMem())
	if it := set(t, m, "message_size_limit", `8192`); it.PendingRestart || it.Restart {
		t.Errorf("a live setting: %+v", it)
	}
	it := set(t, m, "push.enabled", `false`)
	if !it.Restart || !it.PendingRestart || it.Value != false || it.Default != true {
		t.Errorf("push.enabled: %+v", it)
	}
	if m.Config().Push.Enabled {
		t.Error("the new value is not what Config says")
	}
	// Setting it back means there is nothing to restart for.
	if it := set(t, m, "push.enabled", `true`); it.PendingRestart || it.Overridden {
		t.Errorf("back to what is running: %+v", it)
	}
}

func TestAStoredChoiceIsUsedAfterARestartAndThenNothingIsPending(t *testing.T) {
	st := newMem()
	m := newManager(t, st)
	set(t, m, "push.enabled", `false`)
	set(t, m, "branding.name", `"Acme"`)

	again := newManager(t, st)
	if again.Config().Push.Enabled || again.Config().Branding.Name != "Acme" {
		t.Errorf("after a restart: %+v", again.Config())
	}
	it := item(t, again, "push.enabled")
	if it.PendingRestart || !it.Overridden || it.Value != false || it.Default != true {
		t.Errorf("push.enabled after the restart: %+v", it)
	}
}

func TestSettingTheConfigurationsOwnValueIsNotAnOverride(t *testing.T) {
	st := newMem()
	m := newManager(t, st)
	set(t, m, "message_size_limit", `8192`)
	it := set(t, m, "message_size_limit", `4096`) // what the base says
	if it.Overridden {
		t.Errorf("%+v", it)
	}
	if _, ok := st.data["admin.message_size_limit"]; ok {
		t.Error("an override equal to the default was kept")
	}
}

func TestResetGoesBackToTheConfiguration(t *testing.T) {
	st := newMem()
	m := newManager(t, st)
	set(t, m, "branding.name", `"Acme"`)
	set(t, m, "push.web_push", `false`)

	it, err := m.Reset(ctx, "branding.name")
	if err != nil || it.Overridden || it.Value != "Flare" {
		t.Fatalf("reset: %+v %v", it, err)
	}
	if m.Config().Branding.Name != "Flare" || len(st.data) != 1 {
		t.Errorf("config %q, stored %v", m.Config().Branding.Name, st.data)
	}
	if _, err := m.Reset(ctx, "branding.name"); err != nil {
		t.Errorf("resetting what is not changed: %v", err)
	}
	if it, _ := m.Reset(ctx, "push.web_push"); it.PendingRestart || !m.Config().Push.WebPush {
		t.Errorf("a push setting reset: %+v", it)
	}
}

func TestValuesThatAreNotValidAreRefusedWithTheReasonAndChangeNothing(t *testing.T) {
	st := newMem()
	m := newManager(t, st)
	before := m.Config()
	for _, tc := range []struct{ key, value, mention string }{
		{"branding.name", `""`, "Server name must be 1 to 50"},
		{"branding.name", `"` + strings.Repeat("x", 51) + `"`, "Server name must be 1 to 50"},
		{"branding.name", `42`, "must be text"},
		{"branding.name", `"two\nlines"`, "one line"},
		{"branding.primary_color", `"orange"`, "Accent colour"},
		{"branding.primary_color", `"#12345"`, "Accent colour must be 7"},
		{"branding.app_url_scheme", `"Has Space"`, "App link scheme"},
		{"base_url", `"not a url"`, "Public address"},
		{"base_url", `"ftp://example.com"`, "must be a full http(s) address"},
		{"auth.default_access", `"everything"`, "must be one of read-write"},
		{"auth.default_access", `true`, "must be one of"},
		{"message_size_limit", `0`, "between 1 and 65536"},
		{"message_size_limit", `70000`, "between 1 and 65536"},
		{"message_size_limit", `12.5`, "whole number"},
		{"message_size_limit", `"big"`, "whole number"},
		{"cache_duration", `30`, "between 60 and"},
		{"cache_duration", `999999999`, "between 60 and"},
		{"push.enabled", `"yes"`, "true or false"},
		{"push.vapid_subject", `"not a contact"`, "mailto: address or an https: URL"},
		{"push.allowed_relays", `["ftp://nope"]`, "Push relays"},
		{"push.allowed_relays", `["https://a.example", "https://a.example"]`, "listed twice"},
		{"push.allowed_relays", `"https://a.example"`, "must be a list"},
		{"push.allowed_relays", `[1]`, "list of text"},
		{"push.allowed_relays", `[` + strings.Repeat(`"https://r.example",`, 25) + `"https://r.example"]`, "at most 20"},
		{"branding.name", `{bad json`, "not valid JSON"},
	} {
		_, err := m.Set(ctx, tc.key, json.RawMessage(tc.value))
		var input *InputError
		if !errors.As(err, &input) {
			t.Errorf("%s = %s: err = %v, want an *InputError", tc.key, tc.value, err)
			continue
		}
		if !strings.Contains(input.Msg, tc.mention) {
			t.Errorf("%s = %s: %q does not mention %q", tc.key, tc.value, input.Msg, tc.mention)
		}
	}
	if !reflect.DeepEqual(m.Config(), before) || len(st.data) != 0 {
		t.Errorf("a refused value changed something: %v, %v", m.Config(), st.data)
	}
}

func TestAnUnknownSettingIsNotFound(t *testing.T) {
	m := newManager(t, newMem())
	if _, err := m.Set(ctx, "listen", json.RawMessage(`":9"`)); !errors.Is(err, ErrUnknown) {
		t.Errorf("set: %v", err)
	}
	if _, err := m.Reset(ctx, "database"); !errors.Is(err, ErrUnknown) {
		t.Errorf("reset: %v", err)
	}
}

func TestWhatTheStoreCannotKeepIsNotApplied(t *testing.T) {
	st := newMem()
	m := newManager(t, st)
	st.fail = errors.New("disk full")
	if _, err := m.Set(ctx, "branding.name", json.RawMessage(`"Acme"`)); err == nil {
		t.Fatal("no error")
	}
	if m.Config().Branding.Name != "Flare" {
		t.Error("the change was applied although it could not be stored")
	}
}

func TestAStoredChoiceThatNoLongerApplies(t *testing.T) {
	st := newMem()
	st.data["admin.message_size_limit"] = "999999"        // too large
	st.data["admin.removed_setting"] = `"x"`              // from another version
	st.data["admin.branding.name"] = `{broken`            // not JSON
	st.data["admin.branding.primary_color"] = `"#00AA00"` // fine
	st.data["vapid_key"] = "not ours"                     // another part of the server
	m := newManager(t, st)

	if len(m.Warnings) != 3 {
		t.Errorf("warnings = %v", m.Warnings)
	}
	c := m.Config()
	if c.MessageSizeLimit != 4096 || c.Branding.Name != "Flare" || c.Branding.PrimaryColor != "#00AA00" {
		t.Errorf("config = %+v", c)
	}
}

func TestSubscribersAreToldWhatChanged(t *testing.T) {
	m := newManager(t, newMem())
	var olds, news []config.Config
	m.Subscribe(func(o, n config.Config) { olds, news = append(olds, o), append(news, n) })

	set(t, m, "message_size_limit", `8192`)
	m.Set(ctx, "message_size_limit", json.RawMessage(`0`)) // refused: nobody is told
	if _, err := m.Reset(ctx, "message_size_limit"); err != nil {
		t.Fatal(err)
	}
	if len(news) != 2 || olds[0].MessageSizeLimit != 4096 || news[0].MessageSizeLimit != 8192 || olds[1].MessageSizeLimit != 8192 || news[1].MessageSizeLimit != 4096 {
		t.Errorf("olds %v news %v", olds, news)
	}
}

func TestListsKeepTheirOrderAndNeverBecomeNull(t *testing.T) {
	m := newManager(t, newMem())
	it := item(t, m, "push.allowed_relays")
	raw, _ := json.Marshal(it.Value)
	if string(raw) != "[]" {
		t.Errorf("an empty list is %s", raw)
	}
	set(t, m, "push.allowed_relays", `["https://b.example","https://a.example"]`)
	raw, _ = json.Marshal(item(t, m, "push.allowed_relays").Value)
	if string(raw) != `["https://b.example","https://a.example"]` {
		t.Errorf("order: %s", raw)
	}
}

func TestChangesFromSeveralPlacesAtOnce(t *testing.T) {
	m := newManager(t, newMem())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				switch (i + j) % 3 {
				case 0:
					m.Set(ctx, "message_size_limit", json.RawMessage(`8192`))
				case 1:
					m.Reset(ctx, "message_size_limit")
				default:
					_ = m.List()
					_ = m.Config()
				}
			}
		}(i)
	}
	wg.Wait()
	if n := m.Config().MessageSizeLimit; n != 4096 && n != 8192 {
		t.Errorf("limit = %d", n)
	}
}
