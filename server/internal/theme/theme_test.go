package theme

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func full() Theme {
	colors := map[string]string{}
	for _, t := range Tokens() {
		colors[t.Key] = "#808080"
	}
	// readable: dark text on light surfaces, white text on dark colours
	for _, p := range Pairs() {
		colors[p.Text] = "#000000"
		colors[p.Background] = "#FFFFFF"
	}
	return Theme{Name: "Everything", Brightness: "light", Colors: colors}
}

func small() Theme {
	return Theme{Name: "Midnight", Brightness: "dark", Colors: map[string]string{"primary": "#8ab4f8"}}
}

func TestTheEmbeddedContractIsTheSharedOne(t *testing.T) {
	shared, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "theme-tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shared, Contract()) {
		t.Fatal("internal/theme/theme-tokens.json differs from api/theme-tokens.json: copy the latter over the former")
	}
}

func TestTheContractListsTheColoursInOrder(t *testing.T) {
	want := []string{
		"primary", "on_primary", "primary_container", "on_primary_container",
		"secondary", "on_secondary", "secondary_container", "on_secondary_container",
		"tertiary", "on_tertiary", "tertiary_container", "on_tertiary_container",
		"error", "on_error", "error_container", "on_error_container",
		"background", "surface", "surface_container_lowest", "surface_container_low",
		"surface_container", "surface_container_high", "surface_container_highest",
		"on_surface", "on_surface_variant", "border", "border_subtle",
		"shadow", "scrim", "inverse_surface", "on_inverse_surface", "inverse_primary",
	}
	var got []string
	for _, tok := range Tokens() {
		got = append(got, tok.Key)
		if tok.Label == "" || tok.Help == "" || tok.Group == "" {
			t.Errorf("%s has no label, help or group", tok.Key)
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tokens = %v", got)
	}
	if len(Pairs()) != 12 || MinimumContrast() != 3.0 {
		t.Errorf("pairs = %d, minimum = %v", len(Pairs()), MinimumContrast())
	}
}

func TestContrast(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want float64
	}{
		{"#000000", "#FFFFFF", 21},
		{"#ffffff", "#000000", 21},
		{"#123456", "#123456", 1},
		{"#949494", "#FFFFFF", 3.03},
		{"#757575", "#FFFFFF", 4.6},
	} {
		if got := Contrast(tc.a, tc.b); got < tc.want-0.02 || got > tc.want+0.02 {
			t.Errorf("Contrast(%s, %s) = %.3f, want %.2f", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestKeyIgnoresCaseAndSpace(t *testing.T) {
	if Key("  Midnight ") != "midnight" || Key("MIDNIGHT") != Key("midnight") {
		t.Error("names are not compared by their lower-case, trimmed form")
	}
}

func TestNormalizeAcceptsAThemeAndUpperCasesItsColours(t *testing.T) {
	got, err := Normalize(small())
	if err != nil {
		t.Fatal(err)
	}
	if got.Colors["primary"] != "#8AB4F8" || got.Name != "Midnight" || got.Brightness != "dark" {
		t.Errorf("got %+v", got)
	}
	if _, err := Normalize(full()); err != nil {
		t.Errorf("a theme with every colour: %v", err)
	}
}

func TestNormalizeDoesNotChangeWhatItWasGiven(t *testing.T) {
	in := small()
	_, _ = Normalize(in)
	if in.Colors["primary"] != "#8ab4f8" {
		t.Error("the input map was changed")
	}
}

func TestNormalizeRefuses(t *testing.T) {
	with := func(edit func(*Theme)) Theme {
		th := small()
		th.Colors = map[string]string{"primary": "#8AB4F8"}
		edit(&th)
		return th
	}
	for _, tc := range []struct {
		what    string
		theme   Theme
		mention string
	}{
		{"an empty name", with(func(t *Theme) { t.Name = "" }), "the name must be"},
		{"a name that is too long", with(func(t *Theme) { t.Name = strings.Repeat("a", 33) }), "the name must be"},
		{"a name with a symbol", with(func(t *Theme) { t.Name = "bad;name" }), "the name must be"},
		{"a name starting with a space", with(func(t *Theme) { t.Name = " lead" }), "the name must be"},
		{"a name with a colon", with(func(t *Theme) { t.Name = "a:b" }), "the name must be"},
		{"another brightness", with(func(t *Theme) { t.Brightness = "dim" }), "brightness must be light or dark"},
		{"no brightness", with(func(t *Theme) { t.Brightness = "" }), "brightness must be light or dark"},
		{"a colour a theme cannot set", with(func(t *Theme) { t.Colors["sparkle"] = "#FFFFFF" }), `"sparkle" is not a colour`},
		{"a camelCase colour", with(func(t *Theme) { t.Colors["onPrimary"] = "#FFFFFF" }), `"onPrimary" is not a colour`},
		{"a named colour", with(func(t *Theme) { t.Colors["border"] = "grey" }), "border must be a colour like #RRGGBB"},
		{"a short colour", with(func(t *Theme) { t.Colors["border"] = "#FFF" }), "border must be a colour"},
		{"a colour with alpha", with(func(t *Theme) { t.Colors["border"] = "#FFFFFFFF" }), "border must be a colour"},
		{"a colour without the hash", with(func(t *Theme) { t.Colors["border"] = "FFFFFF" }), "border must be a colour"},
		{"no primary", with(func(t *Theme) { t.Colors = map[string]string{"background": "#000000"} }), "primary is required"},
		{"no colours at all", with(func(t *Theme) { t.Colors = nil }), "primary is required"},
		{"text that cannot be read", with(func(t *Theme) {
			t.Colors["surface"], t.Colors["on_surface"] = "#808080", "#858585"
		}), "on_surface (#858585) on surface (#808080)"},
	} {
		_, err := Normalize(tc.theme)
		var input *InputError
		if !errors.As(err, &input) {
			t.Errorf("%s: err = %v, want an *InputError", tc.what, err)
			continue
		}
		if !strings.Contains(input.Msg, tc.mention) {
			t.Errorf("%s: %q does not mention %q", tc.what, input.Msg, tc.mention)
		}
	}
}

func TestNormalizeChecksReadabilityOnlyWhereBothColoursAreSet(t *testing.T) {
	th := small()
	th.Colors["on_surface"] = "#FFFFFF" // no surface set: the app derives it
	if _, err := Normalize(th); err != nil {
		t.Errorf("one colour of a pair: %v", err)
	}
	th.Colors["surface"], th.Colors["on_surface"] = "#FFFFFF", "#949494" // 3.03:1
	if _, err := Normalize(th); err != nil {
		t.Errorf("just above the minimum: %v", err)
	}
	th.Colors["on_surface"] = "#9E9E9E" // 2.68:1
	if _, err := Normalize(th); err == nil {
		t.Error("just below the minimum was accepted")
	}
}

// --- the service, over a store that lives in memory ---------------------------

type memStore struct {
	themes map[string]Stored
	now    time.Time
}

func newMem() *memStore { return &memStore{themes: map[string]Stored{}} }

func (m *memStore) Themes(context.Context) ([]Stored, error) {
	out := []Stored{}
	for _, t := range m.themes {
		out = append(out, t)
	}
	return out, nil
}

func (m *memStore) CreateTheme(_ context.Context, t Theme, now time.Time, max int) error {
	if len(m.themes) >= max {
		return ErrLimit
	}
	if _, ok := m.themes[Key(t.Name)]; ok {
		return ErrExists
	}
	m.themes[Key(t.Name)] = Stored{Theme: t, CreatedAt: now, UpdatedAt: now}
	return nil
}

func (m *memStore) ReplaceTheme(_ context.Context, t Theme, now time.Time) error {
	old, ok := m.themes[Key(t.Name)]
	if !ok {
		return ErrNotFound
	}
	m.themes[Key(t.Name)] = Stored{Theme: t, CreatedAt: old.CreatedAt, UpdatedAt: now}
	return nil
}

func (m *memStore) DeleteTheme(_ context.Context, key string) error {
	if _, ok := m.themes[key]; !ok {
		return ErrNotFound
	}
	delete(m.themes, key)
	return nil
}

func TestServiceChecksBeforeItStores(t *testing.T) {
	m := newMem()
	s := NewService(m)
	bad := small()
	bad.Brightness = "dim"
	if _, err := s.Create(context.Background(), bad); err == nil || len(m.themes) != 0 {
		t.Errorf("a bad theme was stored: %v, %d", err, len(m.themes))
	}
	if _, err := s.Replace(context.Background(), bad); err == nil {
		t.Error("a bad replacement was accepted")
	}
}

func TestServiceCreatesReplacesAndDeletes(t *testing.T) {
	ctx := context.Background()
	s := NewService(newMem())
	created, err := s.Create(ctx, small())
	if err != nil || created.Colors["primary"] != "#8AB4F8" {
		t.Fatalf("create: %+v %v", created, err)
	}
	if _, err := s.Create(ctx, Theme{Name: "MIDNIGHT", Brightness: "dark", Colors: map[string]string{"primary": "#000000"}}); !errors.Is(err, ErrExists) {
		t.Errorf("a name in another case: %v", err)
	}
	changed := Theme{Name: "midnight", Brightness: "light", Colors: map[string]string{"primary": "#112233"}}
	if _, err := s.Replace(ctx, changed); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].Name != "midnight" || list[0].Brightness != "light" || list[0].Colors["primary"] != "#112233" {
		t.Errorf("after replacing: %+v", list)
	}
	if _, err := s.Replace(ctx, Theme{Name: "Nope", Brightness: "dark", Colors: map[string]string{"primary": "#000000"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("replacing a theme that is not there: %v", err)
	}
	if err := s.Delete(ctx, "  MIDNIGHT "); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := s.Delete(ctx, "midnight"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
}

func TestServiceStopsAtTheLimit(t *testing.T) {
	ctx := context.Background()
	s := NewService(newMem())
	for i := 0; i < MaxThemes; i++ {
		th := small()
		th.Name = "T" + strings.Repeat("x", i%20) + string(rune('a'+i/20)) + string(rune('a'+i%20))
		if _, err := s.Create(ctx, th); err != nil {
			t.Fatalf("theme %d (%s): %v", i, th.Name, err)
		}
	}
	extra := small()
	extra.Name = "OneTooMany"
	if _, err := s.Create(ctx, extra); !errors.Is(err, ErrLimit) {
		t.Errorf("the %dth theme: %v", MaxThemes+1, err)
	}
}
