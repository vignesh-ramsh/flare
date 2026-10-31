// Package settings holds the server's settings that an administrator may change while it
// runs, from the Admin UI.
//
// The server's configuration comes from built-in defaults, a YAML file, environment variables
// and flags, and is read once at start. For the settings listed here, an administrator's
// choice is kept in the database and laid over that: it wins, the file's value is shown beside
// it, and resetting goes back to the file's. Most of them take effect at once; those that
// cannot (push services are set up when the server starts) say they need a restart, and the
// manager says when what is running differs from what is set.
//
// Infrastructure is left to the file: where the server listens, its database, the proxies it
// trusts, and the push credentials, which are secrets.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/config"
)

// Store keeps the settings an administrator has changed.
type Store interface {
	Settings(ctx context.Context, prefix string) (map[string]string, error)
	PutSetting(ctx context.Context, key, value string) error
	DeleteSetting(ctx context.Context, key string) error
}

// prefix is what an administrator's choices are stored under, apart from the server's other
// stored values (such as its Web Push key).
const prefix = "admin."

// Kind says what a setting holds.
type Kind string

const (
	String  Kind = "string"
	Int     Kind = "int"
	Bool    Kind = "bool"
	Enum    Kind = "enum"
	Seconds Kind = "seconds" // a length of time, in whole seconds
	List    Kind = "list"    // a list of strings
)

// Group collects settings that belong together.
type Group struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Groups lists the groups of settings, in the order to show them.
func Groups() []Group {
	return []Group{
		{"branding", "Branding and address"},
		{"access", "Access and limits"},
		{"push", "Push notifications"},
	}
}

// Def describes one setting an administrator may change.
type Def struct {
	Key   string
	Group string
	Label string
	Help  string
	Kind  Kind
	// Enum lists the values of an Enum setting.
	Enum []string
	// Min and Max bound an Int or Seconds setting, or the length of a String; Max also bounds
	// how many items a List holds.
	Min, Max int
	// Restart means the setting takes effect only when the server next starts.
	Restart bool

	// configKey is how config.Validate names this setting in its problems.
	configKey string
	str       func(*config.Config) *string
	num       func(*config.Config) *int
	flag      func(*config.Config) *bool
	dur       func(*config.Config) *time.Duration
	list      func(*config.Config) *[]string
}

func (d Def) get(c config.Config) any {
	switch d.Kind {
	case String, Enum:
		return *d.str(&c)
	case Int:
		return *d.num(&c)
	case Bool:
		return *d.flag(&c)
	case Seconds:
		return int64(*d.dur(&c) / time.Second)
	case List:
		if v := *d.list(&c); v != nil {
			return v
		}
		return []string{}
	}
	panic("settings: unknown kind " + string(d.Kind))
}

// set puts a value decoded by decode into c.
func (d Def) set(c *config.Config, v any) {
	switch d.Kind {
	case String, Enum:
		*d.str(c) = v.(string)
	case Int:
		*d.num(c) = v.(int)
	case Bool:
		*d.flag(c) = v.(bool)
	case Seconds:
		*d.dur(c) = time.Duration(v.(int64)) * time.Second
	case List:
		*d.list(c) = append([]string(nil), v.([]string)...)
	}
}

// InputError is a value that is not valid for its setting, in words an admin can act on.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func bad(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

// decode reads raw as the kind of value d holds and checks what it can without the rest of
// the configuration.
func (d Def) decode(raw json.RawMessage) (any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, bad("%s: the value is not valid JSON", d.Label)
	}
	switch d.Kind {
	case String:
		s, ok := v.(string)
		if !ok {
			return nil, bad("%s must be text", d.Label)
		}
		s = strings.TrimSpace(s)
		if n := len([]rune(s)); n < d.Min || n > d.Max {
			return nil, bad("%s must be %d to %d characters (found %d)", d.Label, d.Min, d.Max, n)
		}
		return s, nil
	case Enum:
		s, ok := v.(string)
		if !ok {
			return nil, bad("%s must be one of %s", d.Label, strings.Join(d.Enum, ", "))
		}
		for _, e := range d.Enum {
			if s == e {
				return s, nil
			}
		}
		return nil, bad("%s must be one of %s (found %q)", d.Label, strings.Join(d.Enum, ", "), s)
	case Bool:
		b, ok := v.(bool)
		if !ok {
			return nil, bad("%s must be true or false", d.Label)
		}
		return b, nil
	case Int, Seconds:
		f, ok := v.(float64)
		if !ok || f != float64(int64(f)) {
			return nil, bad("%s must be a whole number", d.Label)
		}
		if f < float64(d.Min) || f > float64(d.Max) {
			return nil, bad("%s must be between %d and %d (found %d)", d.Label, d.Min, d.Max, int64(f))
		}
		if d.Kind == Int {
			return int(f), nil
		}
		return int64(f), nil
	case List:
		items, ok := v.([]any)
		if !ok {
			return nil, bad("%s must be a list", d.Label)
		}
		if len(items) > d.Max {
			return nil, bad("%s may have at most %d entries", d.Label, d.Max)
		}
		out := []string{}
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, bad("%s must be a list of text", d.Label)
			}
			if s = strings.TrimSpace(s); s != "" {
				if len(s) > 300 {
					return nil, bad("an entry of %s is too long", d.Label)
				}
				out = append(out, s)
			}
		}
		return out, nil
	}
	return nil, bad("%s: unsupported", d.Label)
}

// Defs lists the settings, in the order to show them.
func Defs() []Def { return append([]Def(nil), defs...) }

var defs = []Def{
	{Key: "branding.name", Group: "branding", Label: "Server name", Kind: String, Min: 1, Max: 50, configKey: "branding.name",
		Help: "What this server calls itself in apps, next to the app's own name.",
		str:  func(c *config.Config) *string { return &c.Branding.Name }},
	{Key: "branding.primary_color", Group: "branding", Label: "Accent colour", Kind: String, Min: 7, Max: 7, configKey: "branding.primary-color",
		Help: "A colour like #F97316, shown with the server's name in apps and on its pages.",
		str:  func(c *config.Config) *string { return &c.Branding.PrimaryColor }},
	{Key: "branding.app_url_scheme", Group: "branding", Label: "App link scheme", Kind: String, Min: 2, Max: 32, configKey: "branding.app-url-scheme",
		Help: "The URL scheme of the app that opens this server's subscribe links. It must match the url_scheme of the app users install.",
		str:  func(c *config.Config) *string { return &c.Branding.AppURLScheme }},
	{Key: "base_url", Group: "branding", Label: "Public address", Kind: String, Min: 0, Max: 200, configKey: "base-url",
		Help: "Where this server is reached from outside, such as https://flare.example.com. Used in the links the server shows. Leave empty if unknown.",
		str:  func(c *config.Config) *string { return &c.BaseURL }},

	{Key: "auth.default_access", Group: "access", Label: "What anyone may do by default", Kind: Enum, configKey: "auth.default-access",
		Enum: []string{"read-write", "read-only", "write-only", "deny-all"},
		Help: "Where no access rule says otherwise. deny-all makes every topic private until a user is given access.",
		str:  func(c *config.Config) *string { return &c.Auth.DefaultAccess }},
	{Key: "message_size_limit", Group: "access", Label: "Largest message", Kind: Int, Min: 1, Max: 65536, configKey: "message-size-limit",
		Help: "The most bytes a message body may have.",
		num:  func(c *config.Config) *int { return &c.MessageSizeLimit }},
	{Key: "cache_duration", Group: "access", Label: "How long messages are kept", Kind: Seconds, Min: 60, Max: 365 * 24 * 3600, configKey: "cache-duration",
		Help: "How many seconds a message stays available for a subscriber that was offline to catch up. Applies to messages published from now on.",
		dur:  func(c *config.Config) *time.Duration { return &c.CacheDuration }},

	{Key: "push.enabled", Group: "push", Label: "Push notifications", Kind: Bool, Restart: true, configKey: "push.enabled",
		Help: "Wake apps that are not connected, through their platform's push service. When off, the server takes no devices and sends nothing.",
		flag: func(c *config.Config) *bool { return &c.Push.Enabled }},
	{Key: "push.content_free", Group: "push", Label: "Keep message text out of push services", Kind: Bool, Restart: true, configKey: "push.content-free",
		Help: "Tell devices only that a message is waiting, so Google, Apple and other push services never see its text.",
		flag: func(c *config.Config) *bool { return &c.Push.ContentFree }},
	{Key: "push.web_push", Group: "push", Label: "Web Push", Kind: Bool, Restart: true, configKey: "push.web-push",
		Help: "Push to browsers. Needs no credentials.",
		flag: func(c *config.Config) *bool { return &c.Push.WebPush }},
	{Key: "push.unified_push", Group: "push", Label: "UnifiedPush", Kind: Bool, Restart: true, configKey: "push.unified-push",
		Help: "Push through a UnifiedPush distributor. Needs no credentials.",
		flag: func(c *config.Config) *bool { return &c.Push.UnifiedPush }},
	{Key: "push.allow_private_endpoints", Group: "push", Label: "Allow private push endpoints", Kind: Bool, Restart: true, configKey: "push.allow-private-endpoints",
		Help: "Let Web Push and UnifiedPush endpoints be on private networks and use plain http, for a distributor on your own network. Otherwise only public https addresses are used.",
		flag: func(c *config.Config) *bool { return &c.Push.AllowPrivateEndpoints }},
	{Key: "push.vapid_subject", Group: "push", Label: "Contact for push services", Kind: String, Min: 0, Max: 200, Restart: true, configKey: "push.vapid-subject",
		Help: "A mailto: address or https: URL where a Web Push service can reach you. Defaults to the public address.",
		str:  func(c *config.Config) *string { return &c.Push.VAPIDSubject }},
	{Key: "push.allowed_relays", Group: "push", Label: "Push relays", Kind: List, Max: 20, Restart: true, configKey: "push.allowed-relays",
		Help: "Relays this server may ask to wake devices of apps it holds no push credentials for, one address per entry.",
		list: func(c *config.Config) *[]string { return &c.Push.AllowedRelays }},
}

var byKey = func() map[string]Def {
	m := make(map[string]Def, len(defs))
	for _, d := range defs {
		if _, dup := m[d.Key]; dup {
			panic("settings: key listed twice: " + d.Key)
		}
		m[d.Key] = d
	}
	return m
}()

// Item is a setting with its value and where that value comes from, as the Admin UI shows it.
type Item struct {
	Key     string   `json:"key"`
	Group   string   `json:"group"`
	Label   string   `json:"label"`
	Help    string   `json:"help"`
	Type    Kind     `json:"type"`
	Enum    []string `json:"enum,omitempty"`
	Min     *int     `json:"min,omitempty"`
	Max     *int     `json:"max,omitempty"`
	Restart bool     `json:"restart"`
	// Value is what is set now.
	Value any `json:"value"`
	// Default is what the configuration file, environment and flags say, which is what
	// resetting goes back to.
	Default any `json:"default"`
	// Overridden says an administrator has changed it from Default.
	Overridden bool `json:"overridden"`
	// PendingRestart says the value differs from what the running server uses, so it applies
	// after a restart.
	PendingRestart bool `json:"pending_restart"`
}

// ErrUnknown means there is no such setting.
var ErrUnknown = errors.New("there is no such setting")

// Manager holds the effective configuration: the base one with the administrator's choices
// laid over it.
type Manager struct {
	store Store

	mu        sync.Mutex // serialises changes
	base      config.Config
	overrides map[string]string // key to the value as JSON
	running   config.Config     // what the server started with, for settings that need a restart
	eff       atomic.Pointer[config.Config]

	subMu sync.Mutex
	subs  []func(old, new config.Config)

	// Warnings are stored choices that could not be applied when the server started.
	Warnings []string
}

// New returns a Manager over base (the configuration from the file, environment and flags) and
// the choices stored in st. A stored choice that is no longer valid is left out and reported in
// Warnings, so a bad value can never stop the server from starting.
func New(ctx context.Context, st Store, base config.Config) (*Manager, error) {
	stored, err := st.Settings(ctx, prefix)
	if err != nil {
		return nil, err
	}
	m := &Manager{store: st, base: base, overrides: map[string]string{}}
	cur := base
	for full, value := range stored {
		key := strings.TrimPrefix(full, prefix)
		def, ok := byKey[key]
		if !ok {
			m.Warnings = append(m.Warnings, fmt.Sprintf("the stored setting %q is not one this version knows; ignored", key))
			continue
		}
		next, err := m.applied(cur, def, json.RawMessage(value))
		if err != nil {
			m.Warnings = append(m.Warnings, fmt.Sprintf("the stored setting %q no longer applies (%v); using the configuration's", key, err))
			continue
		}
		cur = next
		m.overrides[key] = value
	}
	m.running = cur
	m.eff.Store(&cur)
	return m, nil
}

// applied returns c with the setting def set to raw, or an *InputError if that is not valid.
func (m *Manager) applied(c config.Config, def Def, raw json.RawMessage) (config.Config, error) {
	v, err := def.decode(raw)
	if err != nil {
		return c, err
	}
	def.set(&c, v)
	if err := c.Validate(); err != nil {
		return c, bad("%s", problemsFor(err.Error(), def))
	}
	return c, nil
}

// problemsFor picks from config.Validate's list the lines about def, in words an admin can read.
func problemsFor(all string, def Def) string {
	var mine []string
	for _, line := range strings.Split(all, "\n") {
		if strings.HasPrefix(line, def.configKey+":") || strings.HasPrefix(line, def.configKey+"[") {
			_, msg, _ := strings.Cut(line, ": ")
			mine = append(mine, strings.TrimSpace(msg))
		}
	}
	if len(mine) == 0 {
		return def.Label + " is not valid: " + strings.ReplaceAll(all, "\n", "; ")
	}
	return def.Label + " " + strings.Join(mine, "; ")
}

// Config returns the configuration as it is now: what the running server should use.
func (m *Manager) Config() config.Config { return *m.eff.Load() }

// Subscribe calls fn whenever the configuration changes, with the old and the new one, after
// the change is in effect. fn must not block.
func (m *Manager) Subscribe(fn func(old, new config.Config)) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	m.subs = append(m.subs, fn)
}

func (m *Manager) publish(old, cur config.Config) {
	m.subMu.Lock()
	subs := append([]func(old, new config.Config){}, m.subs...)
	m.subMu.Unlock()
	for _, fn := range subs {
		fn(old, cur)
	}
}

func canonical(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (m *Manager) item(def Def, eff config.Config) Item {
	value := def.get(eff)
	_, overridden := m.overrides[def.Key]
	it := Item{
		Key: def.Key, Group: def.Group, Label: def.Label, Help: def.Help, Type: def.Kind,
		Enum: def.Enum, Restart: def.Restart,
		Value: value, Default: def.get(m.base), Overridden: overridden,
	}
	if def.Kind == Int || def.Kind == Seconds {
		lo, hi := def.Min, def.Max
		it.Min, it.Max = &lo, &hi
	}
	if def.Kind == String {
		lo, hi := def.Min, def.Max
		it.Min, it.Max = &lo, &hi
	}
	it.PendingRestart = def.Restart && !reflect.DeepEqual(def.get(m.running), value)
	return it
}

// List returns every setting with its value.
func (m *Manager) List() []Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	eff := m.Config()
	out := make([]Item, len(defs))
	for i, d := range defs {
		out[i] = m.item(d, eff)
	}
	return out
}

// Set changes a setting. It returns an *InputError for a value that is not valid, and
// ErrUnknown for a setting that does not exist. Nothing changes unless the whole
// configuration stays valid.
func (m *Manager) Set(ctx context.Context, key string, raw json.RawMessage) (Item, error) {
	def, ok := byKey[key]
	if !ok {
		return Item{}, ErrUnknown
	}
	m.mu.Lock()
	old := m.Config()
	next, err := m.applied(old, def, raw)
	if err != nil {
		m.mu.Unlock()
		return Item{}, err
	}
	// Setting it to what the configuration already says is the same as not changing it.
	if reflect.DeepEqual(def.get(next), def.get(m.base)) {
		if err := m.store.DeleteSetting(ctx, prefix+key); err != nil {
			m.mu.Unlock()
			return Item{}, err
		}
		delete(m.overrides, key)
	} else {
		stored := canonical(def.get(next))
		if err := m.store.PutSetting(ctx, prefix+key, stored); err != nil {
			m.mu.Unlock()
			return Item{}, err
		}
		m.overrides[key] = stored
	}
	m.eff.Store(&next)
	it := m.item(def, next)
	m.mu.Unlock()
	m.publish(old, next)
	return it, nil
}

// Reset takes a setting back to what the configuration says.
func (m *Manager) Reset(ctx context.Context, key string) (Item, error) {
	def, ok := byKey[key]
	if !ok {
		return Item{}, ErrUnknown
	}
	m.mu.Lock()
	if err := m.store.DeleteSetting(ctx, prefix+key); err != nil {
		m.mu.Unlock()
		return Item{}, err
	}
	delete(m.overrides, key)
	old := m.Config()
	next := old
	def.set(&next, def.get(m.base))
	m.eff.Store(&next)
	it := m.item(def, next)
	m.mu.Unlock()
	m.publish(old, next)
	return it, nil
}
