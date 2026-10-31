package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server.yml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsAreValid(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("the built-in defaults are invalid: %v", err)
	}
	if !c.Open() {
		t.Error("the default access should be open")
	}
}

func TestLoadWithoutAFileGivesDefaults(t *testing.T) {
	c, err := Load("")
	if err != nil || !reflect.DeepEqual(c, Default()) {
		t.Errorf("Load(\"\") = %+v, %v", c, err)
	}
}

func TestFileOverridesOnlyWhatItSets(t *testing.T) {
	c, err := Load(write(t, `
# a comment
base-url: https://flare.example.com/
cache-duration: 12h
auth:
  default-access: read-only
branding:
  name: Acme Alerts
`))
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.BaseURL = "https://flare.example.com/"
	want.CacheDuration = 12 * time.Hour
	want.Auth.DefaultAccess = "read-only"
	want.Branding.Name = "Acme Alerts"
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got  %+v\nwant %+v", c, want)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "https://flare.example.com" {
		t.Errorf("Validate should drop the trailing slash, got %q", c.BaseURL)
	}
}

func TestAnEmptyFileMeansDefaults(t *testing.T) {
	for _, content := range []string{"", "\n", "# only comments\n"} {
		c, err := Load(write(t, content))
		if err != nil || !reflect.DeepEqual(c, Default()) {
			t.Errorf("Load(%q) = %+v, %v", content, c, err)
		}
	}
}

func TestMistakesInTheFileAreErrors(t *testing.T) {
	tests := map[string]struct{ content, mention string }{
		"misspelled key":             {"listn: :9000\n", "listn"},
		"misspelled nested key":      {"auth:\n  default-acess: deny-all\n", "default-acess"},
		"not supported yet (push)":   {"push:\n  apps: []\n", "push"},
		"not supported yet (attach)": {"attachments:\n  store: file:///x\n", "attachments"},
		"duplicate key":              {"listen: :1\nlisten: :2\n", "already defined"},
		"wrong type":                 {"message-size-limit: lots\n", "message-size-limit"},
		"duration without a unit":    {"cache-duration: 86400\n", "cache-duration"},
		"not YAML":                   {"listen: [unclosed\n", "listen"},
		"a list instead of a map":    {"- a\n- b\n", "config file"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.mention) {
				t.Errorf("err = %v, want it to mention %q", err, tt.mention)
			}
		})
	}
}

func TestLoadReportsAMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yml"))
	if err == nil || !strings.Contains(err.Error(), "nope.yml") {
		t.Errorf("err = %v, want it to name the file", err)
	}
}

func TestEnvironmentOverridesTheFile(t *testing.T) {
	c, err := Load(write(t, "listen: :1111\nbase-url: https://from-file.example\n"))
	if err != nil {
		t.Fatal(err)
	}
	err = c.ApplyEnv(env(map[string]string{
		"FLARE_LISTEN":                  "127.0.0.1:2222",
		"FLARE_DATABASE":                "sqlite:///data/f.db",
		"FLARE_CACHE_DURATION":          "36h",
		"FLARE_MESSAGE_SIZE_LIMIT":      "1024",
		"FLARE_AUTH_DEFAULT_ACCESS":     "write-only",
		"FLARE_BRANDING_NAME":           "Env Name",
		"FLARE_BRANDING_PRIMARY_COLOR":  "#112233",
		"FLARE_BRANDING_APP_URL_SCHEME": "envapp",
		"FLARE_TRUSTED_PROXIES":         "10.0.0.0/8, 192.168.1.1",
		"FLARE_UNRELATED":               "ignored",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:2222" || c.Database != "sqlite:///data/f.db" || c.CacheDuration != 36*time.Hour ||
		c.MessageSizeLimit != 1024 || c.Auth.DefaultAccess != "write-only" || c.Branding.Name != "Env Name" ||
		c.Branding.PrimaryColor != "#112233" || c.Branding.AppURLScheme != "envapp" ||
		!reflect.DeepEqual(c.TrustedProxies, []string{"10.0.0.0/8", " 192.168.1.1"}) {
		t.Errorf("environment not applied: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the spaces around a listed proxy should not matter: %v", err)
	}
	if c.BaseURL != "https://from-file.example" {
		t.Errorf("a setting with no environment variable was changed: %q", c.BaseURL)
	}
}

func TestUnsetEnvironmentChangesNothing(t *testing.T) {
	c := Default()
	if err := c.ApplyEnv(env(nil)); err != nil || !reflect.DeepEqual(c, Default()) {
		t.Errorf("got %+v, %v", c, err)
	}
}

func TestBadEnvironmentValuesAreErrors(t *testing.T) {
	c := Default()
	err := c.ApplyEnv(env(map[string]string{"FLARE_CACHE_DURATION": "soon", "FLARE_MESSAGE_SIZE_LIMIT": "big"}))
	if err == nil || !strings.Contains(err.Error(), "FLARE_CACHE_DURATION") || !strings.Contains(err.Error(), "FLARE_MESSAGE_SIZE_LIMIT") {
		t.Errorf("err = %v, want both variables named", err)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(*Config)
		mention string // "" means valid
	}{
		{"defaults", func(*Config) {}, ""},
		{"https base url", func(c *Config) { c.BaseURL = "https://flare.example.com" }, ""},
		{"base url with a path", func(c *Config) { c.BaseURL = "https://example.com/flare" }, ""},
		{"base url without scheme", func(c *Config) { c.BaseURL = "flare.example.com" }, "base-url"},
		{"base url with ftp", func(c *Config) { c.BaseURL = "ftp://example.com" }, "base-url"},
		{"base url with credentials", func(c *Config) { c.BaseURL = "https://u:p@example.com" }, "credentials"},
		{"base url with a query", func(c *Config) { c.BaseURL = "https://example.com/?a=1" }, "query"},
		{"listen all interfaces", func(c *Config) { c.Listen = ":9000" }, ""},
		{"listen loopback", func(c *Config) { c.Listen = "127.0.0.1:9000" }, ""},
		{"listen ipv6", func(c *Config) { c.Listen = "[::1]:9000" }, ""},
		{"listen without a port", func(c *Config) { c.Listen = "localhost" }, "listen"},
		{"listen with a huge port", func(c *Config) { c.Listen = ":99999" }, "port"},
		{"listen with a named port", func(c *Config) { c.Listen = ":http" }, "port"},
		{"absolute sqlite path", func(c *Config) { c.Database = "sqlite:///var/lib/flare/flare.db" }, ""},
		{"relative sqlite path", func(c *Config) { c.Database = "sqlite://data/flare.db" }, ""},
		{"empty sqlite path", func(c *Config) { c.Database = "sqlite://" }, "file path"},
		{"postgres", func(c *Config) { c.Database = "postgres://u@h/db" }, "not supported yet"},
		{"unknown database", func(c *Config) { c.Database = "mysql://x" }, "sqlite://"},
		{"no database", func(c *Config) { c.Database = "" }, "database"},
		{"cache of one minute", func(c *Config) { c.CacheDuration = time.Minute }, ""},
		{"cache under a minute", func(c *Config) { c.CacheDuration = 30 * time.Second }, "cache-duration"},
		{"cache of zero", func(c *Config) { c.CacheDuration = 0 }, "cache-duration"},
		{"cache over a year", func(c *Config) { c.CacheDuration = 366 * 24 * time.Hour }, "cache-duration"},
		{"message limit of 65536", func(c *Config) { c.MessageSizeLimit = 65536 }, ""},
		{"message limit too large", func(c *Config) { c.MessageSizeLimit = 65537 }, "message-size-limit"},
		{"message limit of zero", func(c *Config) { c.MessageSizeLimit = 0 }, "message-size-limit"},
		{"unknown access", func(c *Config) { c.Auth.DefaultAccess = "open" }, "auth.default-access"},
		{"empty access", func(c *Config) { c.Auth.DefaultAccess = "" }, "auth.default-access"},
		{"no trusted proxies", func(c *Config) { c.TrustedProxies = nil }, ""},
		{"a proxy address", func(c *Config) { c.TrustedProxies = []string{"10.0.0.5", "::1"} }, ""},
		{"a proxy range", func(c *Config) { c.TrustedProxies = []string{"172.16.0.0/12", "fd00::/8"} }, ""},
		{"a proxy by name", func(c *Config) { c.TrustedProxies = []string{"caddy"} }, "trusted-proxies"},
		{"a bad proxy range", func(c *Config) { c.TrustedProxies = []string{"10.0.0.0/33"} }, "trusted-proxies"},
		{"an empty proxy", func(c *Config) { c.TrustedProxies = []string{"10.0.0.1", ""} }, "trusted-proxies"},
		{"empty name", func(c *Config) { c.Branding.Name = "" }, "branding.name"},
		{"name with a newline", func(c *Config) { c.Branding.Name = "a\nb" }, "branding.name"},
		{"unicode name", func(c *Config) { c.Branding.Name = "Café Ünï ✓" }, ""},
		{"bad colour", func(c *Config) { c.Branding.PrimaryColor = "orange" }, "branding.primary-color"},
		{"upper-case scheme", func(c *Config) { c.Branding.AppURLScheme = "Flare" }, "branding.app-url-scheme"},
		{"hyphenated scheme", func(c *Config) { c.Branding.AppURLScheme = "acme-alerts" }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Default()
			tt.edit(&c)
			err := c.Validate()
			switch {
			case tt.mention == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.mention != "" && (err == nil || !strings.Contains(err.Error(), tt.mention)):
				t.Errorf("err = %v, want it to mention %q", err, tt.mention)
			}
		})
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	c := Default()
	c.Listen, c.CacheDuration, c.Auth.DefaultAccess = "nowhere", 0, "everything"
	err := c.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	if lines := strings.Split(err.Error(), "\n"); len(lines) != 3 {
		t.Errorf("got %d problems, want 3:\n%v", len(lines), err)
	}
}

func TestDatabasePath(t *testing.T) {
	for in, want := range map[string]string{
		"sqlite:///var/lib/flare/flare.db": "/var/lib/flare/flare.db",
		"sqlite://flare.db":                "flare.db",
		"sqlite://./a/b.db":                "./a/b.db",
	} {
		c := Config{Database: in}
		if got, err := c.DatabasePath(); err != nil || got != want {
			t.Errorf("DatabasePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestOpen(t *testing.T) {
	for access, want := range map[string]bool{"read-write": true, "read-only": true, "write-only": true, "deny-all": false, "garbage": false} {
		c := Default()
		c.Auth.DefaultAccess = access
		if c.Open() != want {
			t.Errorf("Open() with %q = %v, want %v", access, c.Open(), want)
		}
	}
}

// The sample in deploy/ is what people copy, so it must always be valid.
func TestTheSampleConfigurationIsValid(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "..", "deploy", "server.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	d := Default()
	d.BaseURL = "https://flare.example.com"
	d.Database = "sqlite:///var/lib/flare/flare.db"
	d.TrustedProxies = []string{"172.16.0.0/12", "192.168.0.0/16"} // for Docker Compose
	d.Auth.DefaultAccess = "deny-all"                              // the sample is for a server on the internet
	if !reflect.DeepEqual(c, d) {
		t.Errorf("the sample states values that differ from the defaults it claims to show, apart from the ones this test lists:\n got  %+v\n want %+v", c, d)
	}
}

func TestTrustedProxiesFromTheFile(t *testing.T) {
	c, err := Load(write(t, "trusted-proxies:\n  - 172.16.0.0/12\n  - 10.0.0.5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.TrustedProxies, []string{"172.16.0.0/12", "10.0.0.5"}) {
		t.Errorf("TrustedProxies = %q", c.TrustedProxies)
	}
	if _, err := Load(write(t, "trusted-proxies: 10.0.0.0/8\n")); err == nil {
		t.Error("a single string where a list is expected should be an error")
	}
}

func TestTheEnvironmentReplacesTheListOfTrustedProxies(t *testing.T) {
	c, err := Load(write(t, "trusted-proxies: [10.0.0.5]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyEnv(env(map[string]string{"FLARE_TRUSTED_PROXIES": "192.168.0.0/16"})); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.TrustedProxies, []string{"192.168.0.0/16"}) {
		t.Errorf("TrustedProxies = %q, want the environment's list alone", c.TrustedProxies)
	}
}
