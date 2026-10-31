package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

func openSettings(t *testing.T) *SQLite {
	t.Helper()
	s, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "flare.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSettingsAreStoredReplacedAndDeleted(t *testing.T) {
	s := openSettings(t)
	if err := s.PutSetting(ctx, "admin.a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSetting(ctx, "admin.a", "2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Settings(ctx, "admin."); !reflect.DeepEqual(got, map[string]string{"admin.a": "2"}) {
		t.Errorf("after replacing: %v", got)
	}
	if err := s.DeleteSetting(ctx, "admin.a"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSetting(ctx, "admin.a"); err != nil {
		t.Errorf("deleting what is not there: %v", err)
	}
	if got, _ := s.Settings(ctx, "admin."); len(got) != 0 {
		t.Errorf("after deleting: %v", got)
	}
}

func TestOnlySettingsUnderThePrefixAreReturned(t *testing.T) {
	s := openSettings(t)
	for k, v := range map[string]string{"admin.x": "1", "admin.y": "2", "vapid_key": "secret", "administrator": "3", "a_min.z": "4", "a%": "5"} {
		if err := s.PutSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Settings(ctx, "admin.")
	if err != nil || !reflect.DeepEqual(got, map[string]string{"admin.x": "1", "admin.y": "2"}) {
		t.Errorf("admin. = %v, %v", got, err)
	}
	// Characters that mean something to LIKE mean nothing here.
	if got, _ := s.Settings(ctx, "a_"); !reflect.DeepEqual(got, map[string]string{"a_min.z": "4"}) {
		t.Errorf("a_ = %v", got)
	}
	if got, _ := s.Settings(ctx, "a%"); !reflect.DeepEqual(got, map[string]string{"a%": "5"}) {
		t.Errorf("a%% = %v", got)
	}
}

func TestASettingNeedsAName(t *testing.T) {
	if err := openSettings(t).PutSetting(ctx, "  ", "x"); err == nil {
		t.Error("a setting with no name was stored")
	}
}

func TestTheServersOwnStoredValuesAreNotDisturbed(t *testing.T) {
	s := openSettings(t)
	if _, err := s.SetSettingIfAbsent(ctx, "vapid_key", "k"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSetting(ctx, "admin.x", "1"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.Setting(ctx, "vapid_key"); !ok || v != "k" {
		t.Errorf("vapid_key = %q, %v", v, ok)
	}
}
