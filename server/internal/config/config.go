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
	"github.com/vignesh-ramsh/flare/server/internal/push"
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
	// TrustedProxies are the reverse proxies in front of the server, as addresses
	// or CIDR ranges. Only they are believed when they say, in X-Forwarded-For,
	// who a client is; the server needs that to count failed logins per client.
	TrustedProxies []string `yaml:"trusted-proxies"`

	Auth     Auth     `yaml:"auth"`
	Push     Push     `yaml:"push"`
	Relay    Relay    `yaml:"relay"`
	Branding Branding `yaml:"branding"`
}

// Push configures push notifications: waking the apps of devices that are not
// connected, through the push service of their platform.
type Push struct {
	// Enabled turns push notifications on. When off, the server takes no devices
	// and sends nothing.
	Enabled bool `yaml:"enabled"`
	// ContentFree keeps message content out of push services: devices are told only
	// that a message is waiting, and fetch it from this server. Without it, a
	// message that fits goes in the notification, which lets the push service (Google,
	// Apple, a browser vendor, a UnifiedPush distributor) read it.
	ContentFree bool `yaml:"content-free"`
	// WebPush and UnifiedPush enable those services. They need no credentials.
	WebPush     bool `yaml:"web-push"`
	UnifiedPush bool `yaml:"unified-push"`
	// VAPIDSubject says how a Web Push service can reach the operator: a mailto: or
	// https: URL. Defaults to base-url.
	VAPIDSubject string `yaml:"vapid-subject"`
	// AllowPrivateEndpoints lets Web Push and UnifiedPush endpoints be on private
	// networks and use plain http, for a UnifiedPush distributor on the same network.
	// Otherwise the server refuses to send anywhere but public https addresses.
	AllowPrivateEndpoints bool `yaml:"allow-private-endpoints"`
	// AllowedRelays are the push relays this server will ask to wake devices of
	// apps it holds no credentials for (see the push relay in the architecture). A
	// device can register through a relay only if it is listed here, so devices can't
	// make the server send requests to addresses of their choosing.
	AllowedRelays []string `yaml:"allowed-relays"`
	// Apps are the apps this server holds Firebase and Apple credentials for. Each
	// credential belongs to one app's publisher, so a server can push only to the apps
	// listed here; for other apps a relay does it.
	Apps []PushApp `yaml:"apps"`
}

// Relay configures `flare relay`, which wakes devices for servers that don't hold
// their app's push credentials. The relay uses listen, trusted-proxies and
// push.apps from the rest of the configuration.
type Relay struct {
	// Database is where the relay keeps the handles apps have registered. It is not
	// the server's database.
	Database string `yaml:"database"`
	// The limits, per minute: wake-ups for one handle, wake-ups and registrations
	// from one client address.
	WakesPerHandlePerMinute     int `yaml:"wakes-per-handle-per-minute"`
	WakesPerIPPerMinute         int `yaml:"wakes-per-ip-per-minute"`
	RegistrationsPerIPPerMinute int `yaml:"registrations-per-ip-per-minute"`
}

// PushApp holds the push credentials of one app.
type PushApp struct {
	// AppID is the Android application ID and Apple bundle ID of the app.
	AppID string `yaml:"app-id"`
	// FCMCredentials is the path of the Firebase service account key (JSON).
	FCMCredentials string `yaml:"fcm-credentials"`
	// APNsKey is the path of the Apple .p8 key, with its ID and the team's.
	APNsKey    string `yaml:"apns-key"`
	APNsKeyID  string `yaml:"apns-key-id"`
	APNsTeamID string `yaml:"apns-team-id"`
	// APNsSandbox uses Apple's development servers, for apps built for development.
	APNsSandbox bool `yaml:"apns-sandbox"`
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
		Push:             Push{Enabled: true, WebPush: true, UnifiedPush: true},
		Relay:            Relay{Database: "sqlite://relay.db", WakesPerHandlePerMinute: 30, WakesPerIPPerMinute: 6000, RegistrationsPerIPPerMinute: 30},
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
	if v := getenv("FLARE_TRUSTED_PROXIES"); v != "" {
		c.TrustedProxies = strings.Split(v, ",")
	}
	str("FLARE_PUSH_VAPID_SUBJECT", &c.Push.VAPIDSubject)
	for name, dst := range map[string]*bool{
		"FLARE_PUSH_ENABLED":                 &c.Push.Enabled,
		"FLARE_PUSH_CONTENT_FREE":            &c.Push.ContentFree,
		"FLARE_PUSH_WEB_PUSH":                &c.Push.WebPush,
		"FLARE_PUSH_UNIFIED_PUSH":            &c.Push.UnifiedPush,
		"FLARE_PUSH_ALLOW_PRIVATE_ENDPOINTS": &c.Push.AllowPrivateEndpoints,
	} {
		if v := getenv(name); v != "" {
			if b, err := strconv.ParseBool(v); err != nil {
				errs = append(errs, fmt.Errorf("%s: %q is not true or false", name, v))
			} else {
				*dst = b
			}
		}
	}
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
	if _, err := auth.ParseAccess(c.Auth.DefaultAccess); err != nil {
		bad("auth.default-access", "%v", err)
	}
	if _, err := auth.ParseProxies(c.TrustedProxies); err != nil {
		bad("trusted-proxies", "%v", err)
	}

	c.validatePush(bad)

	if _, err := c.RelayDatabasePath(); err != nil {
		bad("relay.database", "%v", err)
	}
	for key, n := range map[string]int{
		"relay.wakes-per-handle-per-minute":     c.Relay.WakesPerHandlePerMinute,
		"relay.wakes-per-ip-per-minute":         c.Relay.WakesPerIPPerMinute,
		"relay.registrations-per-ip-per-minute": c.Relay.RegistrationsPerIPPerMinute,
	} {
		if n < 1 || n > 1_000_000 {
			bad(key, "must be between 1 and 1000000 (found %d)", n)
		}
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
func (c *Config) DatabasePath() (string, error) { return sqlitePath(c.Database) }

// RelayDatabasePath returns the file the relay's SQLite database lives in.
func (c *Config) RelayDatabasePath() (string, error) { return sqlitePath(c.Relay.Database) }

func sqlitePath(database string) (string, error) {
	if path, ok := strings.CutPrefix(database, "sqlite://"); ok {
		if path == "" {
			return "", errors.New("sqlite:// needs a file path, like sqlite:///var/lib/flare/flare.db")
		}
		return path, nil
	}
	if strings.HasPrefix(database, "postgres://") || strings.HasPrefix(database, "postgresql://") {
		return "", errors.New("PostgreSQL is not supported yet; use sqlite://<path>")
	}
	return "", fmt.Errorf("must start with sqlite:// (found %q)", database)
}

// Open reports whether anyone who can reach the server may publish or subscribe
// without logging in.
func (c *Config) Open() bool {
	a, err := auth.ParseAccess(c.Auth.DefaultAccess)
	return err == nil && a.Open()
}

// validatePush checks the push settings. It does not read the credential files;
// the server does that when it starts, and says which could not be read.
func (c *Config) validatePush(bad func(key, format string, args ...any)) {
	p := c.Push
	if s := p.VAPIDSubject; s != "" {
		if !strings.HasPrefix(s, "mailto:") && !strings.HasPrefix(s, "https://") {
			bad("push.vapid-subject", "must be a mailto: address or an https: URL (found %q)", s)
		} else if len(s) > 200 {
			bad("push.vapid-subject", "must be at most 200 characters")
		}
	}
	relays := map[string]bool{}
	for i, r := range p.AllowedRelays {
		norm, err := push.NormalizeRelayURL(r)
		switch {
		case err != nil:
			bad(fmt.Sprintf("push.allowed-relays[%d]", i), "%v", err)
		case relays[norm]:
			bad(fmt.Sprintf("push.allowed-relays[%d]", i), "%q is listed twice", r)
		default:
			relays[norm] = true
		}
	}
	seen := map[string]bool{}
	for i, app := range p.Apps {
		key := fmt.Sprintf("push.apps[%d]", i)
		switch {
		case !push.ValidAppID(app.AppID):
			bad(key+".app-id", "must be the app's identifier, such as org.example.flare (found %q)", app.AppID)
			continue
		case seen[app.AppID]:
			bad(key+".app-id", "%q is listed twice", app.AppID)
			continue
		}
		seen[app.AppID] = true
		if app.FCMCredentials == "" && app.APNsKey == "" {
			bad(key, "%s needs fcm-credentials or apns-key, or both", app.AppID)
		}
		if app.APNsKey != "" && (app.APNsKeyID == "" || app.APNsTeamID == "") {
			bad(key, "%s: apns-key needs apns-key-id and apns-team-id", app.AppID)
		}
		if app.APNsKey == "" && (app.APNsKeyID != "" || app.APNsTeamID != "" || app.APNsSandbox) {
			bad(key, "%s: apns-key-id, apns-team-id and apns-sandbox only mean something with apns-key", app.AppID)
		}
	}
}

// Credentials returns the app's push credentials in the form the push package
// reads them.
func (a PushApp) Credentials() push.App {
	return push.App{
		AppID: a.AppID, FCMCredentials: a.FCMCredentials,
		APNsKey: a.APNsKey, APNsKeyID: a.APNsKeyID, APNsTeamID: a.APNsTeamID, APNsSandbox: a.APNsSandbox,
	}
}
