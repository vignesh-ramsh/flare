package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/theme"
)

const midnight = `{"name":"Midnight","brightness":"dark","colors":{"primary":"#8ab4f8","background":"#0B1220","surface":"#111A2B","on_surface":"#E6EDF7"}}`

type themeList struct {
	Themes []struct {
		Name       string            `json:"name"`
		Brightness string            `json:"brightness"`
		Colors     map[string]string `json:"colors"`
		Created    int64             `json:"created"`
		Updated    int64             `json:"updated"`
	} `json:"themes"`
}

func (e *env) themes() themeList {
	e.t.Helper()
	resp, data := e.do("GET", "/v1/themes", "")
	if resp.StatusCode != 200 {
		e.t.Fatalf("GET /v1/themes = %d %s", resp.StatusCode, data)
	}
	var out themeList
	decodeInto(e.t, data, &out)
	return out
}

// admin is an environment with an administrator and an ordinary user, and the
// administrator's token.
func newAdminEnv(t *testing.T, edit ...func(*config.Config)) (*env, []string) {
	t.Helper()
	e := newEnv(t, edit...)
	e.addUser("root", auth.RoleAdmin)
	e.addUser("alice", auth.RoleUser)
	secret, _ := e.tokenFor("root")
	return e, bearer(secret)
}

func TestTheThemeListIsEmptyAtFirst(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do("GET", "/v1/themes", "")
	if resp.StatusCode != 200 || strings.TrimSpace(string(data)) != `{"themes":[]}` {
		t.Errorf("%d %s", resp.StatusCode, data)
	}
}

func TestTheThemeListIsPublicEvenWhereTopicsAreNot(t *testing.T) {
	e, admin := newAdminEnv(t, denyAll())
	if resp, data := e.do("POST", "/v1/admin/themes", midnight, admin...); resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, data)
	}
	if got := e.themes(); len(got.Themes) != 1 || got.Themes[0].Name != "Midnight" {
		t.Errorf("anonymous list on a closed server: %+v", got)
	}
	resp, _ := e.do("GET", "/v1/themes/schema", "")
	if resp.StatusCode != 200 {
		t.Errorf("schema = %d", resp.StatusCode)
	}
}

func TestAnAdministratorAddsATheme(t *testing.T) {
	e, admin := newAdminEnv(t)
	resp, data := e.do("POST", "/v1/admin/themes", midnight, admin...)
	if resp.StatusCode != 201 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	var created struct {
		Name       string            `json:"name"`
		Brightness string            `json:"brightness"`
		Colors     map[string]string `json:"colors"`
	}
	decodeInto(t, data, &created)
	if created.Colors["primary"] != "#8AB4F8" {
		t.Errorf("colours are stored in upper case: %+v", created)
	}

	list := e.themes()
	if len(list.Themes) != 1 {
		t.Fatalf("list = %+v", list)
	}
	got := list.Themes[0]
	if got.Name != "Midnight" || got.Brightness != "dark" || got.Colors["background"] != "#0B1220" || got.Created == 0 || got.Updated == 0 {
		t.Errorf("listed = %+v", got)
	}
}

func TestThemesAreListedByName(t *testing.T) {
	e, admin := newAdminEnv(t)
	for _, name := range []string{"Zebra", "apple", "Mango"} {
		body := fmt.Sprintf(`{"name":%q,"brightness":"light","colors":{"primary":"#112233"}}`, name)
		if resp, data := e.do("POST", "/v1/admin/themes", body, admin...); resp.StatusCode != 201 {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, data)
		}
	}
	var names []string
	for _, th := range e.themes().Themes {
		names = append(names, th.Name)
	}
	if strings.Join(names, ",") != "apple,Mango,Zebra" {
		t.Errorf("order = %v", names)
	}
}

func TestOnlyAnAdministratorMayChangeThemes(t *testing.T) {
	e, admin := newAdminEnv(t)
	e.do("POST", "/v1/admin/themes", midnight, admin...)
	alice, _ := e.tokenFor("alice")

	requests := []struct{ method, path, body string }{
		{"POST", "/v1/admin/themes", midnight},
		{"PUT", "/v1/admin/themes/Midnight", midnight},
		{"DELETE", "/v1/admin/themes/Midnight", ""},
	}
	for _, rq := range requests {
		resp, data := e.do(rq.method, rq.path, rq.body)
		e.expectError(resp, data, apierr.Unauthorized, "log in")
		resp, data = e.do(rq.method, rq.path, rq.body, bearer(alice)...)
		e.expectError(resp, data, apierr.Forbidden, "administrator")
		resp, data = e.do(rq.method, rq.path, rq.body, bearer("flr_"+strings.Repeat("x", 43))...)
		e.expectError(resp, data, apierr.InvalidCredentials, "")
	}
	if got := e.themes(); len(got.Themes) != 1 || got.Themes[0].Brightness != "dark" {
		t.Errorf("a refused request changed something: %+v", got)
	}
}

func TestAnAdministratorMayUseAPasswordToo(t *testing.T) {
	e, _ := newAdminEnv(t)
	resp, data := e.do("POST", "/v1/admin/themes", midnight, "Authorization", basicAuth("root", pw))
	if resp.StatusCode != 201 {
		t.Errorf("%d %s", resp.StatusCode, data)
	}
}

func TestABadThemeIsRefusedWithTheReason(t *testing.T) {
	e, admin := newAdminEnv(t)
	for _, tc := range []struct{ what, body, mention string }{
		{"a bad name", `{"name":"bad;name","brightness":"dark","colors":{"primary":"#112233"}}`, "the name must be"},
		{"no name", `{"brightness":"dark","colors":{"primary":"#112233"}}`, "the name must be"},
		{"a bad brightness", `{"name":"X","brightness":"dim","colors":{"primary":"#112233"}}`, "brightness must be"},
		{"a colour that is not one", `{"name":"X","brightness":"dark","colors":{"primary":"#112233","border":"grey"}}`, "border must be a colour"},
		{"an unknown colour", `{"name":"X","brightness":"dark","colors":{"primary":"#112233","sparkle":"#FFFFFF"}}`, "not a colour a theme can set"},
		{"no primary", `{"name":"X","brightness":"dark","colors":{"background":"#112233"}}`, "primary is required"},
		{"no colours", `{"name":"X","brightness":"dark"}`, "primary is required"},
		{"unreadable text", `{"name":"X","brightness":"dark","colors":{"primary":"#112233","surface":"#808080","on_surface":"#858585"}}`, "contrast"},
	} {
		resp, data := e.do("POST", "/v1/admin/themes", tc.body, admin...)
		e.expectError(resp, data, apierr.InvalidParameter, tc.mention)
		if resp.StatusCode != 400 {
			t.Errorf("%s: status %d", tc.what, resp.StatusCode)
		}
	}
	if n := len(e.themes().Themes); n != 0 {
		t.Errorf("%d themes were stored", n)
	}
}

func TestABodyThatIsNotAThemeIsRefused(t *testing.T) {
	e, admin := newAdminEnv(t)
	for _, body := range []string{"", "not json", `[1,2]`, `{"name":"X","brightness":"dark","colors":{"primary":"#112233"},"extra":1}`, `{"name":"X","colors":"red"}`} {
		resp, data := e.do("POST", "/v1/admin/themes", body, admin...)
		e.expectError(resp, data, apierr.InvalidBody, "")
	}
}

func TestATooLargeBodyIsRefused(t *testing.T) {
	e, admin := newAdminEnv(t)
	big := `{"name":"X","brightness":"dark","colors":{"primary":"#112233"},"pad":"` + strings.Repeat("a", maxThemeBody) + `"}`
	resp, data := e.do("POST", "/v1/admin/themes", big, admin...)
	e.expectError(resp, data, apierr.TooLarge, "too large")
}

func TestANameThatIsTakenIsRefusedWhateverItsCase(t *testing.T) {
	e, admin := newAdminEnv(t)
	e.do("POST", "/v1/admin/themes", midnight, admin...)
	for _, name := range []string{"Midnight", "midnight", "MIDNIGHT"} {
		body := fmt.Sprintf(`{"name":%q,"brightness":"light","colors":{"primary":"#112233"}}`, name)
		resp, data := e.do("POST", "/v1/admin/themes", body, admin...)
		e.expectError(resp, data, apierr.AlreadyExists, "already exists")
		if resp.StatusCode != 409 {
			t.Errorf("%s: status %d", name, resp.StatusCode)
		}
	}
}

func TestAServerHoldsOnlySomanyThemes(t *testing.T) {
	e, admin := newAdminEnv(t)
	for i := 0; i < theme.MaxThemes; i++ {
		body := fmt.Sprintf(`{"name":"T%03d","brightness":"light","colors":{"primary":"#112233"}}`, i)
		if resp, data := e.do("POST", "/v1/admin/themes", body, admin...); resp.StatusCode != 201 {
			t.Fatalf("theme %d: %d %s", i, resp.StatusCode, data)
		}
	}
	resp, data := e.do("POST", "/v1/admin/themes", `{"name":"OneMore","brightness":"light","colors":{"primary":"#112233"}}`, admin...)
	e.expectError(resp, data, apierr.LimitReached, "delete one first")
	if resp.StatusCode != 429 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestReplacingATheme(t *testing.T) {
	e, admin := newAdminEnv(t)
	e.do("POST", "/v1/admin/themes", midnight, admin...)
	before := e.themes().Themes[0]

	// The list's own answer can be sent back to edit: the read-only fields are ignored.
	edited := fmt.Sprintf(`{"name":"MIDNIGHT","brightness":"light","colors":{"primary":"#112233"},"created":%d,"updated":%d}`, before.Created, before.Updated)
	resp, data := e.do("PUT", "/v1/admin/themes/Midnight", edited, admin...)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	got := e.themes().Themes
	if len(got) != 1 || got[0].Name != "MIDNIGHT" || got[0].Brightness != "light" || got[0].Colors["primary"] != "#112233" || len(got[0].Colors) != 1 {
		t.Errorf("after replacing: %+v", got)
	}
	if got[0].Created != before.Created {
		t.Errorf("created changed: %d -> %d", before.Created, got[0].Created)
	}
}

func TestReplacingFindsTheThemeByAnyCaseAndAnEncodedName(t *testing.T) {
	e, admin := newAdminEnv(t)
	e.do("POST", "/v1/admin/themes", `{"name":"High contrast","brightness":"dark","colors":{"primary":"#FFD60A"}}`, admin...)
	path := "/v1/admin/themes/" + url.PathEscape("high CONTRAST")
	resp, data := e.do("PUT", path, `{"name":"High contrast","brightness":"dark","colors":{"primary":"#FFFF00"}}`, admin...)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if got := e.themes().Themes[0].Colors["primary"]; got != "#FFFF00" {
		t.Errorf("primary = %s", got)
	}
}

func TestAThemeCannotBeRenamedOrReplacedWhenMissing(t *testing.T) {
	e, admin := newAdminEnv(t)
	e.do("POST", "/v1/admin/themes", midnight, admin...)
	resp, data := e.do("PUT", "/v1/admin/themes/Midnight", `{"name":"Dusk","brightness":"dark","colors":{"primary":"#112233"}}`, admin...)
	e.expectError(resp, data, apierr.InvalidParameter, "cannot be renamed")
	resp, data = e.do("PUT", "/v1/admin/themes/Nope", `{"name":"Nope","brightness":"dark","colors":{"primary":"#112233"}}`, admin...)
	e.expectError(resp, data, apierr.NotFound, "no such theme")
	if got := e.themes().Themes; len(got) != 1 || got[0].Name != "Midnight" {
		t.Errorf("list = %+v", got)
	}
}

func TestDeletingATheme(t *testing.T) {
	e, admin := newAdminEnv(t)
	e.do("POST", "/v1/admin/themes", midnight, admin...)
	resp, _ := e.do("DELETE", "/v1/admin/themes/midnight", "", admin...)
	if resp.StatusCode != 204 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if n := len(e.themes().Themes); n != 0 {
		t.Errorf("%d left", n)
	}
	resp, data := e.do("DELETE", "/v1/admin/themes/midnight", "", admin...)
	e.expectError(resp, data, apierr.NotFound, "no such theme")
}

func TestTheListSaysWhenNothingHasChanged(t *testing.T) {
	e, admin := newAdminEnv(t)
	resp, _ := e.do("GET", "/v1/themes", "")
	empty := resp.Header.Get("ETag")
	if empty == "" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("etag %q, cache %q", empty, resp.Header.Get("Cache-Control"))
	}
	resp, data := e.do("GET", "/v1/themes", "", "If-None-Match", empty)
	if resp.StatusCode != 304 || len(data) != 0 {
		t.Errorf("unchanged: %d %q", resp.StatusCode, data)
	}

	e.do("POST", "/v1/admin/themes", midnight, admin...)
	resp, _ = e.do("GET", "/v1/themes", "", "If-None-Match", empty)
	added := resp.Header.Get("ETag")
	if resp.StatusCode != 200 || added == empty {
		t.Errorf("after adding: %d, etag %q (was %q)", resp.StatusCode, added, empty)
	}
	if resp, _ = e.do("GET", "/v1/themes", "", "If-None-Match", added); resp.StatusCode != 304 {
		t.Errorf("unchanged again: %d", resp.StatusCode)
	}
}

func TestTheSchemaIsTheSharedContract(t *testing.T) {
	e := newEnv(t)
	resp, data := e.do("GET", "/v1/themes/schema", "")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var got struct {
		Tokens []struct{ Key string } `json:"tokens"`
		Pairs  []any                  `json:"readable_pairs"`
	}
	decodeInto(t, data, &got)
	if len(got.Tokens) != 32 || len(got.Pairs) != 12 || got.Tokens[0].Key != "primary" {
		t.Errorf("schema = %d tokens, %d pairs", len(got.Tokens), len(got.Pairs))
	}
}

func TestThemesAreAFeatureOfTheServer(t *testing.T) {
	e := newEnv(t)
	_, data := e.do("GET", "/v1/config", "")
	var cfg struct{ Features []string }
	decodeInto(t, data, &cfg)
	found := false
	for _, f := range cfg.Features {
		found = found || f == "themes"
	}
	if !found {
		t.Errorf("features = %v", cfg.Features)
	}
}

func TestAServerWithoutThemesSaysSo(t *testing.T) {
	a := New(Deps{Config: config.Default()})
	for _, rq := range []struct{ method, path string }{{"GET", "/v1/themes"}, {"POST", "/v1/admin/themes"}, {"DELETE", "/v1/admin/themes/x"}} {
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest(rq.method, rq.path, strings.NewReader("{}")))
		var body struct{ Code int }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != apierr.Unsupported || w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: %d %s", rq.method, rq.path, w.Code, w.Body.String())
		}
	}
}
