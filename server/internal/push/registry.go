package push

import (
	"sort"
	"strings"
)

// Registry knows which push services this server can use, and how. FCM and APNs
// credentials belong to one app, so those senders are registered per app ID; Web
// Push and UnifiedPush need none, and serve every app; a relay is chosen by its
// address.
type Registry struct {
	senders map[key]Sender
}

type key struct {
	provider Provider
	scope    string // the app ID (FCM, APNs), the relay's address (relay), or "" (the rest)
}

// NewRegistry returns a Registry with no senders.
func NewRegistry() *Registry { return &Registry{senders: map[key]Sender{}} }

// AddApp registers the sender that reaches devices of one app through FCM or APNs.
func (r *Registry) AddApp(p Provider, appID string, s Sender) { r.senders[key{p, appID}] = s }

// Add registers the sender for a provider that serves every app: Web Push and
// UnifiedPush.
func (r *Registry) Add(p Provider, s Sender) { r.senders[key{p, ""}] = s }

// AddRelay registers the sender that wakes devices through the relay at url.
func (r *Registry) AddRelay(url string, s Sender) { r.senders[key{Relay, url}] = s }

func scopeOf(d Device) string {
	switch d.Provider {
	case FCM, APNs:
		return d.AppID
	case Relay:
		return d.RelayURL
	}
	return ""
}

// For returns the sender that reaches d.
func (r *Registry) For(d Device) (Sender, bool) {
	s, ok := r.senders[key{d.Provider, scopeOf(d)}]
	return s, ok
}

// Supports reports whether a device registered like d could be reached.
func (r *Registry) Supports(d Device) bool {
	_, ok := r.For(d)
	return ok
}

// Description is what the server tells apps about how it can push, through
// /v1/config.
type Description struct {
	// Providers are the services every app can use: "webpush", "unifiedpush".
	Providers []string
	// Apps are the app IDs the server holds credentials for, by provider.
	Apps map[string][]string
	// Relays are the relays the server will wake devices through.
	Relays []string
	// WebPushKey is the public key browsers subscribe with (the "application server
	// key"), if Web Push is enabled.
	WebPushKey string
}

// KeyHolder is implemented by a Web Push sender, so that the server can tell
// browsers which key to subscribe with.
type KeyHolder interface{ PublicKey() string }

// Describe summarises the registry. Nothing in it is secret: app IDs are public
// bundle identifiers, and relays are web addresses.
func (r *Registry) Describe() Description {
	d := Description{Apps: map[string][]string{}}
	for k, s := range r.senders {
		switch k.provider {
		case WebPush, UnifiedPush:
			d.Providers = append(d.Providers, string(k.provider))
			if kh, ok := s.(KeyHolder); ok {
				d.WebPushKey = kh.PublicKey()
			}
		case FCM, APNs:
			d.Apps[string(k.provider)] = append(d.Apps[string(k.provider)], k.scope)
		case Relay:
			d.Relays = append(d.Relays, k.scope)
		}
	}
	sort.Strings(d.Providers)
	sort.Strings(d.Relays)
	for _, apps := range d.Apps {
		sort.Strings(apps)
	}
	return d
}

// Names lists the providers the registry has a sender for, for log lines.
func (r *Registry) Names() string {
	seen := map[string]bool{}
	for k := range r.senders {
		seen[string(k.provider)] = true
	}
	var names []string
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
