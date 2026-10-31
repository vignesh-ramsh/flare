package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/push"
)

// APIVersion is the version of the HTTP API. Clients of the same version work
// with servers of the same version.
const APIVersion = 1

// features lists what this server can do, so that apps can check for a feature
// instead of comparing version numbers.
var features = []string{"publish", "poll", "sse", "websocket", "accounts"}

// featuresOf adds to features what depends on how the server is set up.
func (a *API) featuresOf() []string {
	out := append([]string{}, features...)
	if a.Themes != nil {
		out = append(out, "themes")
	}
	if a.Push != nil {
		out = append(out, "push")
	}
	return out
}

// index is GET /: a hint for someone who opened the server in a browser.
func (a *API) index(w http.ResponseWriter, r *http.Request) {
	cfg := a.config()
	base := cfg.BaseURL
	if base == "" {
		base = "http://" + r.Host
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "%s is running.\n\nPublish:   curl -d \"hello\" %s/mytopic\nSubscribe: curl -s %s/mytopic/json\n",
		cfg.Branding.Name, base, base)
}

// healthz is GET /healthz: the process is up.
func (a *API) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

// readyz is GET /readyz: the server can store messages. Load balancers use it to
// decide whether to send traffic here.
func (a *API) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.Hub.Ready(ctx); err != nil {
		a.Log.Warn("not ready", "error", err)
		writeError(w, apierr.Unavailable, "the message store is not available")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

// serverConfig is GET /v1/config: what an app needs to know to show and use this
// server. It reveals nothing an anonymous visitor couldn't find out anyway.
func (a *API) serverConfig(w http.ResponseWriter, r *http.Request) {
	c := a.config()
	var body struct {
		APIVersion   int      `json:"api_version"`
		Name         string   `json:"name"`
		PrimaryColor string   `json:"primary_color"`
		AppURLScheme string   `json:"app_url_scheme"`
		BaseURL      string   `json:"base_url,omitempty"`
		Features     []string `json:"features"`
		Limits       struct {
			MessageSize   int   `json:"message_size"`   // bytes
			CacheDuration int64 `json:"cache_duration"` // seconds
		} `json:"limits"`
		Auth struct {
			DefaultAccess string `json:"default_access"`
		} `json:"auth"`
		Push *pushInfo `json:"push,omitempty"`
	}
	body.APIVersion = APIVersion
	body.Name = c.Branding.Name
	body.PrimaryColor = c.Branding.PrimaryColor
	body.AppURLScheme = c.Branding.AppURLScheme
	body.BaseURL = c.BaseURL
	body.Features = a.featuresOf()
	body.Limits.MessageSize = c.MessageSizeLimit
	body.Limits.CacheDuration = int64(c.CacheDuration / time.Second)
	body.Auth.DefaultAccess = c.Auth.DefaultAccess
	if a.Push != nil {
		body.Push = describePush(a.Push.Registry().Describe())
	}
	writeJSON(w, http.StatusOK, body)
}

// pushInfo tells an app how this server can push to it.
type pushInfo struct {
	// Providers are the services that work for every app: "webpush", "unifiedpush".
	Providers []string `json:"providers"`
	// Apps lists, for "fcm" and "apns", the app IDs the server holds credentials for.
	Apps map[string][]string `json:"apps,omitempty"`
	// Relays are the relays the server wakes devices through.
	Relays []string `json:"relays,omitempty"`
	// WebPushKey is the public key a browser subscribes with.
	WebPushKey string `json:"web_push_public_key,omitempty"`
}

func describePush(d push.Description) *pushInfo {
	info := &pushInfo{Providers: d.Providers, Relays: d.Relays, WebPushKey: d.WebPushKey}
	if info.Providers == nil {
		info.Providers = []string{}
	}
	if len(d.Apps) > 0 {
		info.Apps = d.Apps
	}
	return info
}
