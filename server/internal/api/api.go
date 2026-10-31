// Package api is the server's HTTP interface: publishing, the three ways to
// subscribe (streaming JSON, Server-Sent Events, WebSocket), and the small
// endpoints apps and operators use to find out about the server.
//
// The contract is written down in api/openapi.yaml at the repository root; a
// test fails if the routes here and the file disagree.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/admin"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/push"
	"github.com/vignesh-ramsh/flare/server/internal/realtime"
	"github.com/vignesh-ramsh/flare/server/internal/settings"
	"github.com/vignesh-ramsh/flare/server/internal/theme"
)

// Deps are what the API needs to do its work.
type Deps struct {
	Hub  *realtime.Hub
	Auth *auth.Service
	// Push registers devices for push notifications. Nil means the server has
	// none, and the device endpoints say so.
	Push *push.Service
	// Themes are the themes the server offers its apps. Nil means it offers none.
	Themes *theme.Service
	// Settings holds the settings an administrator may change while the server runs. Nil
	// means none may, and Config is what the server uses.
	Settings *settings.Manager
	Config   config.Config
	Log      *slog.Logger

	// Stream tunes the streaming endpoints. Zero values give the defaults.
	Stream realtime.StreamOptions
	// WSPingInterval is how often idle WebSocket clients are pinged.
	WSPingInterval time.Duration
	// Shutdown ends the long-lived connections (streams and WebSockets) when it
	// is cancelled. Ordinary requests, such as a publish in progress, are left to
	// finish. Nil means never.
	Shutdown context.Context
}

// API is the HTTP handler of the server.
type API struct {
	Deps
	mux *http.ServeMux
}

type route struct {
	pattern string // "METHOD /path", as the standard ServeMux reads it
	handler http.HandlerFunc
}

// New builds the handler for the given dependencies.
func New(d Deps) *API {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Shutdown == nil {
		d.Shutdown = context.Background()
	}
	a := &API{Deps: d, mux: http.NewServeMux()}
	for _, r := range a.routes() {
		a.mux.HandleFunc(r.pattern, r.handler)
	}
	// The Admin UI is a set of static files, not part of the API: it is not in routes() or the
	// OpenAPI description, and does nothing the API's own admin endpoints do not check.
	if err := admin.Register(a.mux); err != nil {
		// The files are compiled in, so this is a mistake in the build, found by every test.
		panic(err)
	}
	return a
}

// routes lists every endpoint. The names {topic} and {topics} are what the
// handlers read with PathValue.
func (a *API) routes() []route {
	return []route{
		{"GET /{$}", a.index},
		{"GET /healthz", a.healthz},
		{"GET /readyz", a.readyz},
		{"GET /v1/config", a.serverConfig},
		{"POST /v1/messages", a.publishJSON},
		{"GET /v1/messages/{id}", a.getMessage},
		{"GET /v1/themes", a.listThemes},
		{"GET /v1/themes/schema", a.themeSchema},
		{"GET /v1/admin/users", a.listUsers},
		{"POST /v1/admin/users", a.createUser},
		{"PATCH /v1/admin/users/{name}", a.changeUser},
		{"DELETE /v1/admin/users/{name}", a.deleteUser},
		{"GET /v1/admin/users/{name}/tokens", a.userTokens},
		{"DELETE /v1/admin/users/{name}/tokens/{id}", a.revokeUserToken},
		{"GET /v1/admin/access", a.listAccess},
		{"PUT /v1/admin/access", a.grantAccess},
		{"DELETE /v1/admin/access", a.revokeAccess},
		{"GET /v1/admin/settings", a.listSettings},
		{"PUT /v1/admin/settings/{key}", a.putSetting},
		{"DELETE /v1/admin/settings/{key}", a.resetSetting},
		{"POST /v1/admin/themes", a.createTheme},
		{"PUT /v1/admin/themes/{name}", a.replaceTheme},
		{"DELETE /v1/admin/themes/{name}", a.deleteTheme},
		{"POST /v1/devices", a.registerDevice},
		{"PUT /v1/devices/{id}/subscriptions", a.followTopics},
		{"DELETE /v1/devices/{id}", a.removeDevice},
		{"GET /v1/ws", a.websocket},
		{"POST /v1/auth/login", a.login},
		{"GET /v1/account", a.account},
		{"GET /v1/account/tokens", a.listTokens},
		{"DELETE /v1/account/tokens/{id}", a.deleteToken},
		{"GET /{topics}/json", a.streamJSON},
		{"GET /{topics}/sse", a.streamSSE},
		{"POST /{topic}", a.publishRaw},
		{"PUT /{topic}", a.publishRaw},
	}
}

// Routes returns every "METHOD /path" the API serves, for tests and tooling.
func (a *API) Routes() []string {
	var out []string
	for _, r := range a.routes() {
		out = append(out, r.pattern)
	}
	return out
}

// Handler returns the API wrapped in the middleware every request goes through.
func (a *API) Handler() http.Handler {
	return a.secureHeaders(a.recoverPanics(a.logRequests(a.mux)))
}

// untilShutdown returns r with a context that is also cancelled when the server
// begins shutting down, for handlers that keep a connection open.
func (a *API) untilShutdown(r *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(a.Shutdown, cancel)
	return r.WithContext(ctx), func() { stop(); cancel() }
}
