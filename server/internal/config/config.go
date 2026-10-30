// Package config reads the server's settings: built-in defaults, overridden by a
// YAML file, then by FLARE_* environment variables, then by command-line flags
// (which the caller applies). Anything misspelled or not supported yet is an
// error, so a typo can never leave a setting silently ignored.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
)

// Config is the server's configuration.
type Config struct {
	// BaseURL is the public address of the server, for example
	// https://flare.example.com.
	BaseURL string `yaml:"base-url"`
	// Listen is the address to listen on: ":8080", "127.0.0.1:8080", ...
	Listen string `yaml:"listen"`
	// Database locates the message store: sqlite://<path>.
	Database string `yaml:"database"`
	// CacheDuration is how long messages are kept for subscribers to catch up.
	CacheDuration time.Duration `yaml:"cache-duration"`
	// MessageSizeLimit is the largest message body, in bytes.
	MessageSizeLimit int `yaml:"message-size-limit"`

	Auth     Auth     `yaml:"auth"`
	Branding Branding `yaml:"branding"`
}

// Auth is the access policy.
type Auth struct {
	// DefaultAccess is what anyone may do: read-write, read-only, write-only or
	// deny-all.
	DefaultAccess string `yaml:"default-access"`
}

// Branding is how this server presents itself to apps, through /v1/config.
type Branding struct {
	Name         string `yaml:"name"`
	PrimaryColor string `yaml:"primary-color"`
	// AppURLScheme is the URL scheme of the app that opens this server's
	// subscribe links; it must match the url_scheme of the app users install.
	AppURLScheme string `yaml:"app-url-scheme"`
}

// Default returns the built-in settings.
func Default() Config {
	return Config{
		Listen:           ":8080",
		Database:         "sqlite://flare.db",
		CacheDuration:    24 * time.Hour,
		MessageSizeLimit: 4096,
		Auth:             Auth{DefaultAccess: string(auth.ReadWrite)},
		Branding:         Branding{Name: "Flare", PrimaryColor: "#F97316", AppURLScheme: "flare"},
	}
}

// Load returns the defaults with the YAML file at path laid over them. An empty
// path means no file.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true) // unknown keys are errors
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Default(), fmt.Errorf("config file %s:\n%w", path, explain(err, src))
	}
	return cfg, nil
}

var (
	lineRef      = regexp.MustCompile(`line (\d+)`)
	unknownField = regexp.MustCompile(`field (\S+) not found in type \S+`)
)

// explain turns the YAML library's messages, which name a line but not the
// setting, into ones a person editing the file can act on: it says when a key is
// unknown and quotes the line concerned.
func explain(err error, src []byte) error {
	var msgs []string
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		msgs = append(msgs, typeErr.Errors...)
	} else {
		msgs = append(msgs, strings.TrimPrefix(err.Error(), "yaml: "))
	}

	lines := strings.Split(string(src), "\n")
	for i, msg := range msgs {
		msg = unknownField.ReplaceAllString(msg, "$1 is not a known setting (misspelled, or not supported yet)")
		if strings.Contains(msg, "into time.Duration") {
			msg += "; durations need a unit, like 24h or 90m"
		}
		if m := lineRef.FindStringSubmatch(msg); m != nil {
			if n, _ := strconv.Atoi(m[1]); n >= 1 && n <= len(lines) {
				msg += " (" + strings.TrimSpace(lines[n-1]) + ")"
			}
		}
		msgs[i] = msg
	}
	return errors.New(strings.Join(msgs, "\n"))
}

// ApplyEnv overrides settings from FLARE_* environment variables. getenv is
// os.Getenv in production.
func (c *Config) ApplyEnv(getenv func(string) string) error {
	var errs []error
	str := func(name string, dst *string) {
		if v := getenv(name); v != "" {
			*dst = v
		}
	}
	str("FLARE_BASE_URL", &c.BaseURL)
	str("FLARE_LISTEN", &c.Listen)
	str("FLARE_DATABASE", &c.Database)
	str("FLARE_AUTH_DEFAULT_ACCESS", &c.Auth.DefaultAccess)
	str("FLARE_BRANDING_NAME", &c.Branding.Name)
	str("FLARE_BRANDING_PRIMARY_COLOR", &c.Branding.PrimaryColor)
	str("FLARE_BRANDING_APP_URL_SCHEME", &c.Branding.AppURLScheme)
	if v := getenv("FLARE_CACHE_DURATION"); v != "" {
		if d, err := time.ParseDuration(v); err != nil {
			errs = append(errs, fmt.Errorf("FLARE_CACHE_DURATION: %w", err))
		} else {
			c.CacheDuration = d
		}
	}
	if v := getenv("FLARE_MESSAGE_SIZE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			errs = append(errs, fmt.Errorf("FLARE_MESSAGE_SIZE_LIMIT: %q is not a whole number of bytes", v))
		} else {
			c.MessageSizeLimit = n
		}
	}
	return errors.Join(errs...)
}

var (
	colorPattern  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	schemePattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]{1,31}$`)
)

// Validate checks every setting, and normalises BaseURL. The error lists every
// problem found, one per line.
func (c *Config) Validate() error {
	var problems []string
	bad := func(key, format string, args ...any) {
		problems = append(problems, key+": "+fmt.Sprintf(format, args...))
	}

	if c.BaseURL != "" {
		u, err := url.Parse(c.BaseURL)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			bad("base-url", "must be a full http(s) address like https://flare.example.com (found %q)", c.BaseURL)
		case u.User != nil || u.RawQuery != "" || u.Fragment != "":
			bad("base-url", "must not contain credentials, a query or a fragment")
		default:
			c.BaseURL = strings.TrimSuffix(c.BaseURL, "/")
		}
	}

	if _, port, err := net.SplitHostPort(c.Listen); err != nil {
		bad("listen", "must be host:port or :port, like :8080 (found %q)", c.Listen)
	} else if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		bad("listen", "port %q is not between 0 and 65535", port)
	}

	if _, err := c.DatabasePath(); err != nil {
		bad("database", "%v", err)
	}

	if c.CacheDuration < time.Minute || c.CacheDuration > 365*24*time.Hour {
		bad("cache-duration", "must be between 1m and 8760h (found %v)", c.CacheDuration)
	}
	if c.MessageSizeLimit < 1 || c.MessageSizeLimit > 65536 {
		bad("message-size-limit", "must be between 1 and 65536 bytes (found %d)", c.MessageSizeLimit)
	}
	if _, err := auth.ParseDefaultAccess(c.Auth.DefaultAccess); err != nil {
		bad("auth.default-access", "%v", err)
	}

	b := c.Branding
	if n := utf8.RuneCountInString(b.Name); n < 1 || n > 50 || strings.ContainsFunc(b.Name, func(r rune) bool { return r < 0x20 || r == 0x7F }) {
		bad("branding.name", "must be 1-50 characters on one line")
	}
	if !colorPattern.MatchString(b.PrimaryColor) {
		bad("branding.primary-color", "must be a colour like \"#F97316\" (found %q)", b.PrimaryColor)
	}
	if !schemePattern.MatchString(b.AppURLScheme) {
		bad("branding.app-url-scheme", "must be 2-32 lower-case letters, digits, + . - starting with a letter (found %q)", b.AppURLScheme)
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// DatabasePath returns the file the SQLite database lives in.
func (c *Config) DatabasePath() (string, error) {
	if path, ok := strings.CutPrefix(c.Database, "sqlite://"); ok {
		if path == "" {
			return "", errors.New("sqlite:// needs a file path, like sqlite:///var/lib/flare/flare.db")
		}
		return path, nil
	}
	if strings.HasPrefix(c.Database, "postgres://") || strings.HasPrefix(c.Database, "postgresql://") {
		return "", errors.New("PostgreSQL is not supported yet; use sqlite://<path>")
	}
	return "", fmt.Errorf("must start with sqlite:// (found %q)", c.Database)
}

// Open reports whether anyone who can reach the server may publish or subscribe
// without logging in.
func (c *Config) Open() bool {
	a, err := auth.ParseDefaultAccess(c.Auth.DefaultAccess)
	return err == nil && a.Open()
}
