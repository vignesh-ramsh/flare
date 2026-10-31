package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/theme"
)

func sampleTheme(name string) theme.Theme {
	return theme.Theme{Name: name, Brightness: "dark", Colors: map[string]string{"primary": "#8AB4F8", "background": "#0B1220"}}
}

func openThemes(t *testing.T) *SQLite {
	t.Helper()
	s, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "flare.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestThemesStartEmptyAndListByName(t *testing.T) {
	s := openThemes(t)
	list, err := s.Themes(ctx)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("empty = %v, %v", list, err)
	}
	now := time.Unix(1_800_000_000, 0)
	for _, name := range []string{"Zebra", "apple", "Mango"} {
		if err := s.CreateTheme(ctx, sampleTheme(name), now, 10); err != nil {
			t.Fatal(err)
		}
	}
	list, _ = s.Themes(ctx)
	var names []string
	for _, th := range list {
		names = append(names, th.Name)
	}
	if !reflect.DeepEqual(names, []string{"apple", "Mango", "Zebra"}) {
		t.Errorf("order = %v", names)
	}
}

func TestAThemeComesBackAsItWentIn(t *testing.T) {
	s := openThemes(t)
	created := time.Unix(1_800_000_000, 0)
	in := sampleTheme("Midnight")
	if err := s.CreateTheme(ctx, in, created, 10); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Themes(ctx)
	got := list[0]
	if !reflect.DeepEqual(got.Theme, in) || !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(created) {
		t.Errorf("got %+v, want %+v created at %v", got, in, created)
	}
}

func TestNamesAreUniqueWhateverTheirCase(t *testing.T) {
	s := openThemes(t)
	now := time.Now()
	if err := s.CreateTheme(ctx, sampleTheme("Midnight"), now, 10); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Midnight", "midnight", "MIDNIGHT"} {
		if err := s.CreateTheme(ctx, sampleTheme(name), now, 10); !errors.Is(err, theme.ErrExists) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if list, _ := s.Themes(ctx); len(list) != 1 {
		t.Errorf("%d themes", len(list))
	}
}

func TestTheLimitCountsWhatIsThere(t *testing.T) {
	s := openThemes(t)
	now := time.Now()
	for _, name := range []string{"A", "B"} {
		if err := s.CreateTheme(ctx, sampleTheme(name), now, 2); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateTheme(ctx, sampleTheme("C"), now, 2); !errors.Is(err, theme.ErrLimit) {
		t.Errorf("past the limit: %v", err)
	}
	if err := s.DeleteTheme(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTheme(ctx, sampleTheme("C"), now, 2); err != nil {
		t.Errorf("after making room: %v", err)
	}
}

func TestReplacingKeepsWhenItWasCreatedAndMovesTheUpdateTime(t *testing.T) {
	s := openThemes(t)
	created := time.Unix(1_800_000_000, 0)
	later := created.Add(time.Hour)
	if err := s.CreateTheme(ctx, sampleTheme("Midnight"), created, 10); err != nil {
		t.Fatal(err)
	}
	next := theme.Theme{Name: "MIDNIGHT", Brightness: "light", Colors: map[string]string{"primary": "#112233"}}
	if err := s.ReplaceTheme(ctx, next, later); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Themes(ctx)
	got := list[0]
	if got.Name != "MIDNIGHT" || got.Brightness != "light" || !reflect.DeepEqual(got.Colors, next.Colors) {
		t.Errorf("got %+v", got)
	}
	if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(later) {
		t.Errorf("created %v updated %v", got.CreatedAt, got.UpdatedAt)
	}
	if err := s.ReplaceTheme(ctx, sampleTheme("Nope"), later); !errors.Is(err, theme.ErrNotFound) {
		t.Errorf("replacing what is not there: %v", err)
	}
}

func TestDeletingATheme(t *testing.T) {
	s := openThemes(t)
	if err := s.CreateTheme(ctx, sampleTheme("Midnight"), time.Now(), 10); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTheme(ctx, "midnight"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTheme(ctx, "midnight"); !errors.Is(err, theme.ErrNotFound) {
		t.Errorf("twice: %v", err)
	}
	if list, _ := s.Themes(ctx); len(list) != 0 {
		t.Errorf("%d left", len(list))
	}
}

func TestThemesSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flare.db")
	s, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTheme(ctx, sampleTheme("Midnight"), time.Now(), 10); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if list, _ := s.Themes(ctx); len(list) != 1 || list[0].Name != "Midnight" {
		t.Errorf("after reopening: %+v", list)
	}
}

func TestTheDatabaseRefusesABrightnessThatIsNeitherLightNorDark(t *testing.T) {
	s := openThemes(t)
	_, err := s.db.ExecContext(ctx, `INSERT INTO themes (name_key, name, brightness, colors, created_at, updated_at) VALUES ('x','X','dim','{}',0,0)`)
	if err == nil {
		t.Error("the schema accepted a brightness of dim")
	}
}
