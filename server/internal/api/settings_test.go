package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/settings"
)

type settingsList struct {
	Groups []struct{ ID, Label string } `json:"groups"`

	Settings []struct {
		Key            string   `json:"key"`
		Group          string   `json:"group"`
		Type           string   `json:"type"`
		Enum           []string `json:"enum"`
		Restart        bool     `json:"restart"`
		Value          any      `json:"value"`
		Default        any      `json:"default"`
		Overridden     bool     `json:"overridden"`
		PendingRestart bool     `json:"pending_restart"`
	} `json:"settings"`
	Warnings []string `json:"warnings"`
}

func (e *env) settingsAs(admin []string) settingsList {
	e.t.Helper()
	resp, data := e.do("GET", "/v1/admin/settings", "", admin...)
	if resp.StatusCode != 200 {
		e.t.Fatalf("GET /v1/admin/settings = %d %s", resp.StatusCode, data)
	}
	var out settingsList
	decodeInto(e.t, data, &out)
	return out
}

func (l settingsList) get(t *testing.T, key string) (v struct {
	Value, Default             any
	Overridden, PendingRestart bool
}) {
	t.Helper()
	for _, s := range l.Settings {
		if s.Key == key {
			v.Value, v.Default, v.Overridden, v.PendingRestart = s.Value, s.Default, s.Overridden, s.PendingRestart
			return v
		}
	}
	t.Fatalf("no setting %s", key)
	return v
}

func put(value string) string { return `{"value":` + value + `}` }

func TestAdministratorsSeeEverySettingWithItsGroup(t *testing.T) {
	e, admin := newAdminEnv(t)
	list := e.settingsAs(admin)
	if len(list.Settings) != len(settings.Defs()) || len(list.Groups) != len(settings.Groups()) {
		t.Errorf("%d settings, %d groups", len(list.Settings), len(list.Groups))
	}
	if list.Warnings == nil || len(list.Warnings) != 0 {
		t.Errorf("warnings = %v", list.Warnings)
	}
	groups := map[string]bool{}
	for _, g := range list.Groups {
		groups[g.ID] = true
	}
	kinds := map[string]bool{}
	for _, s := range list.Settings {
		if !groups[s.Group] {
			t.Errorf("%s is in group %q, which is not listed", s.Key, s.Group)
		}
		kinds[s.Type] = true
		if s.Overridden || s.PendingRestart {
			t.Errorf("%s starts changed: %+v", s.Key, s)
		}
	}
	for _, k := range []string{"string", "int", "bool", "enum", "seconds", "list"} {
		if !kinds[k] {
			t.Errorf("no setting of type %s", k)
		}
	}
}

func TestOnlyAdministratorsMaySeeOrChangeSettings(t *testing.T) {
	e, admin := newAdminEnv(t)
	alice, _ := e.tokenFor("alice")
	for _, rq := range []struct{ method, path, body string }{
		{"GET", "/v1/admin/settings", ""},
		{"PUT", "/v1/admin/settings/branding.name", put(`"X"`)},
		{"DELETE", "/v1/admin/settings/branding.name", ""},
	} {
		resp, data := e.do(rq.method, rq.path, rq.body)
		e.expectError(resp, data, apierr.Unauthorized, "log in")
		resp, data = e.do(rq.method, rq.path, rq.body, bearer(alice)...)
		e.expectError(resp, data, apierr.Forbidden, "administrator")
	}
	if got := e.settingsAs(admin).get(t, "branding.name"); got.Overridden {
		t.Errorf("a refused request changed a setting: %+v", got)
	}
}

func TestAChangedSettingShowsEverywhereAtOnce(t *testing.T) {
	e, admin := newAdminEnv(t)
	resp, data := e.do("PUT", "/v1/admin/settings/branding.name", put(`"  Acme Alerts "`), admin...)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	var item struct {
		Value      string
		Default    string
		Overridden bool
	}
	decodeInto(t, data, &item)
	if item.Value != "Acme Alerts" || item.Default != "Flare" || !item.Overridden {
		t.Errorf("item = %+v", item)
	}

	_, data = e.do("GET", "/v1/config", "")
	var cfg struct {
		Name string `json:"name"`
	}
	decodeInto(t, data, &cfg)
	if cfg.Name != "Acme Alerts" {
		t.Errorf("/v1/config says %q", cfg.Name)
	}
	if _, body := e.do("GET", "/", ""); !strings.Contains(string(body), "Acme Alerts is running") {
		t.Errorf("the front page says %q", body)
	}
}

func TestTheMessageLimitChangesWhatPublishingAccepts(t *testing.T) {
	e, admin := newAdminEnv(t)
	if resp, data := e.do("PUT", "/v1/admin/settings/message_size_limit", put(`10`), admin...); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	resp, data := e.do("POST", "/alerts", strings.Repeat("x", 20))
	e.expectError(resp, data, apierr.TooLarge, "limit of 10 bytes")
	if resp, _ := e.do("POST", "/alerts", "small"); resp.StatusCode != 200 {
		t.Errorf("a message within the limit: %d", resp.StatusCode)
	}
	if _, data := e.do("GET", "/v1/config", ""); !strings.Contains(string(data), `"message_size":10`) {
		t.Errorf("/v1/config: %s", data)
	}
}

func TestAResetGoesBackToTheConfiguration(t *testing.T) {
	e, admin := newAdminEnv(t)
	e.do("PUT", "/v1/admin/settings/branding.name", put(`"Acme"`), admin...)
	resp, data := e.do("DELETE", "/v1/admin/settings/branding.name", "", admin...)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	got := e.settingsAs(admin).get(t, "branding.name")
	if got.Overridden || got.Value != "Flare" {
		t.Errorf("after reset: %+v", got)
	}
}

func TestPushSettingsAreSavedButWaitForARestart(t *testing.T) {
	e, admin := newAdminEnv(t)
	resp, data := e.do("PUT", "/v1/admin/settings/push.enabled", put(`false`), admin...)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	got := e.settingsAs(admin).get(t, "push.enabled")
	if got.Value != false || got.Default != true || !got.Overridden || !got.PendingRestart {
		t.Errorf("push.enabled: %+v", got)
	}
}

func TestARefusedValueSaysWhyAndChangesNothing(t *testing.T) {
	e, admin := newAdminEnv(t)
	for _, tc := range []struct{ key, body, mention string }{
		{"message_size_limit", put(`0`), "between 1 and 65536"},
		{"branding.primary_color", put(`"orange"`), "Accent colour"},
		{"auth.default_access", put(`"all"`), "must be one of"},
		{"push.allowed_relays", put(`["ftp://x"]`), "Push relays"},
	} {
		resp, data := e.do("PUT", "/v1/admin/settings/"+tc.key, tc.body, admin...)
		e.expectError(resp, data, apierr.InvalidParameter, tc.mention)
		if resp.StatusCode != 400 {
			t.Errorf("%s: status %d", tc.key, resp.StatusCode)
		}
	}
	for _, s := range e.settingsAs(admin).Settings {
		if s.Overridden {
			t.Errorf("%s was changed by a refused request", s.Key)
		}
	}
}

func TestASettingThatIsNotThereIsNotFound(t *testing.T) {
	e, admin := newAdminEnv(t)
	for _, key := range []string{"listen", "database", "push.apps", "nope"} {
		resp, data := e.do("PUT", "/v1/admin/settings/"+key, put(`"x"`), admin...)
		e.expectError(resp, data, apierr.NotFound, "no such setting")
		resp, data = e.do("DELETE", "/v1/admin/settings/"+key, "", admin...)
		e.expectError(resp, data, apierr.NotFound, "no such setting")
	}
}

func TestABodyThatIsNotAValueIsRefused(t *testing.T) {
	e, admin := newAdminEnv(t)
	for _, body := range []string{"", "{}", `{"value":`, `{"val":1}`, `{"value":1,"extra":2}`, "plain"} {
		resp, data := e.do("PUT", "/v1/admin/settings/message_size_limit", body, admin...)
		e.expectError(resp, data, apierr.InvalidBody, "")
	}
}

func TestAServerWhoseSettingsCannotBeChangedSaysSo(t *testing.T) {
	a := New(Deps{Config: config.Default()})
	for _, rq := range []struct{ method, path string }{{"GET", "/v1/admin/settings"}, {"PUT", "/v1/admin/settings/branding.name"}, {"DELETE", "/v1/admin/settings/branding.name"}} {
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest(rq.method, rq.path, strings.NewReader(`{"value":"x"}`)))
		var body struct{ Code int }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != apierr.Unsupported || w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: %d %s", rq.method, rq.path, w.Code, w.Body.String())
		}
	}
}

func TestEverySettingCanBeChangedAndResetThroughTheAPI(t *testing.T) {
	e, admin := newAdminEnv(t)
	samples := map[string]string{
		"string": `"Sample name"`, "int": `2048`, "bool": `false`, "enum": `"read-only"`, "seconds": `3600`, "list": `["https://relay.example"]`,
	}
	specialStrings := map[string]string{
		"branding.primary_color": `"#123456"`, "branding.app_url_scheme": `"sample-scheme"`,
		"base_url": `"https://sample.example"`, "push.vapid_subject": `"mailto:a@example.com"`,
	}
	for _, d := range settings.Defs() {
		value := samples[string(d.Kind)]
		if v, ok := specialStrings[d.Key]; ok {
			value = v
		}
		if d.Kind == settings.Bool {
			// The opposite of what the configuration says: setting what it already says is not a change.
			value = fmt.Sprint(e.settingsAs(admin).get(t, d.Key).Default == false)
		}
		if resp, data := e.do("PUT", "/v1/admin/settings/"+d.Key, put(value), admin...); resp.StatusCode != 200 {
			t.Errorf("PUT %s %s = %d %s", d.Key, value, resp.StatusCode, data)
			continue
		}
		if !e.settingsAs(admin).get(t, d.Key).Overridden {
			t.Errorf("%s is not overridden after being set", d.Key)
		}
		if resp, data := e.do("DELETE", "/v1/admin/settings/"+d.Key, "", admin...); resp.StatusCode != 200 {
			t.Errorf("DELETE %s = %d %s", d.Key, resp.StatusCode, data)
		}
		if e.settingsAs(admin).get(t, d.Key).Overridden {
			t.Errorf("%s is still overridden after a reset", d.Key)
		}
	}
}
