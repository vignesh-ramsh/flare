// Package relay is a push relay: a small server, run by whoever publishes an app,
// that holds the app's Firebase and Apple credentials so that Flare servers which
// don't have them can still wake the app's devices.
//
// The app gives the relay its push token and gets an opaque handle back, which it
// hands to the Flare servers it uses. To notify the device a server asks the relay
// to wake the handle, naming a subscription and a message by opaque IDs. The relay
// sends a poll request, with no topic and no content; the app then fetches the
// message from its server. So the server never sees the push token, and the relay
// never sees what was published or where.
package relay

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/push"
)

// HandleSecretHeader carries the secret an app was given with its handle.
const HandleSecretHeader = "Handle-Secret"

// HandleSecretPrefix starts every handle secret, so that a leaked one is recognisable.
const HandleSecretPrefix = "flrh_"

const (
	maxBody      = 4 << 10
	sendTimeout  = 30 * time.Second
	defaultTTL   = 24 * time.Hour
	apiVersion   = 1
	bodyDeadline = 15 * time.Second
)

// Config is what a Relay needs.
type Config struct {
	// Senders reach the apps' devices: the Firebase and Apple credentials the
	// relay holds, one sender per app and provider.
	Senders *push.Registry
	Store   *Store
	// Proxies are the reverse proxies in front of the relay, believed about who a
	// client is.
	Proxies auth.ProxyTrust
	// The limits, per minute. A handle that is woken more often than this is being
	// abused or is broken, and one address registering or waking more is too.
	WakesPerHandle, WakesPerIP, RegistrationsPerIP int
	Log                                            *slog.Logger
	Now                                            func() time.Time
}

// Relay is the HTTP handler of a push relay.
type Relay struct {
	Config
	mux                            *http.ServeMux
	wakeHandle, wakeIP, registerIP *Limiter
}

type route struct {
	pattern string
	handler http.HandlerFunc
}

// New returns a Relay.
func New(cfg Config) *Relay {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.WakesPerHandle <= 0 {
		cfg.WakesPerHandle = 30
	}
	if cfg.WakesPerIP <= 0 {
		cfg.WakesPerIP = 6000
	}
	if cfg.RegistrationsPerIP <= 0 {
		cfg.RegistrationsPerIP = 30
	}
	r := &Relay{
		Config: cfg, mux: http.NewServeMux(),
		wakeHandle: NewLimiter(cfg.WakesPerHandle, cfg.Now),
		wakeIP:     NewLimiter(cfg.WakesPerIP, cfg.Now),
		registerIP: NewLimiter(cfg.RegistrationsPerIP, cfg.Now),
	}
	for _, rt := range r.routes() {
		r.mux.HandleFunc(rt.pattern, rt.handler)
	}
	return r
}

func (r *Relay) routes() []route {
	return []route{
		{"GET /healthz", r.healthz},
		{"GET /readyz", r.readyz},
		{"GET /v1/config", r.config},
		{"POST /v1/handles", r.register},
		{"DELETE /v1/handles/{handle}", r.remove},
		{"POST /v1/wake", r.wake},
	}
}

// Routes returns every "METHOD /path" the relay serves, for tests and tooling.
func (r *Relay) Routes() []string {
	var out []string
	for _, rt := range r.routes() {
		out = append(out, rt.pattern)
	}
	return out
}

// Handler returns the relay wrapped in the middleware every request goes through.
func (r *Relay) Handler() http.Handler {
	return r.recoverPanics(r.logRequests(r.mux))
}

// --- middleware --------------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// logRequests logs the route and status of each request, and nothing that
// identifies a handle, an app's user or a device.
func (r *Relay) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(sw, req)
		_, pattern := r.mux.Handler(req)
		if pattern == "" {
			pattern = "unmatched"
		}
		level := slog.LevelInfo
		if sw.status >= 500 {
			level = slog.LevelError
		} else if sw.status >= 400 {
			level = slog.LevelWarn
		}
		r.Log.Log(req.Context(), level, "request", "route", pattern, "status", sw.status, "duration", time.Since(start).Round(time.Millisecond).String())
	})
}

func (r *Relay) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				r.Log.Error("panic while handling a request", "panic", fmt.Sprint(v))
				writeError(w, apierr.Internal, "internal error")
			}
		}()
		next.ServeHTTP(w, req)
	})
}

// --- responses ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, text string) {
	status := apierr.HTTPStatus(code)
	writeJSON(w, status, struct {
		Code  int    `json:"code"`
		HTTP  int    `json:"http"`
		Error string `json:"error"`
	}{code, status, text})
}

// tooMany answers a request over its limit.
func tooMany(w http.ResponseWriter, wait time.Duration, what string) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	writeError(w, apierr.RateLimited, "too many "+what+"; try again later")
}

// readJSON reads a small JSON object. strict refuses fields it doesn't know.
func readJSON(w http.ResponseWriter, req *http.Request, v any, strict bool) bool {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyDeadline))
	defer func() { _ = rc.SetReadDeadline(time.Time{}) }()
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxBody))
	if strict {
		dec.DisallowUnknownFields()
	}
	err := dec.Decode(v)
	if err == nil && dec.More() {
		err = errors.New("unexpected data after the JSON object")
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			writeError(w, apierr.TooLarge, "the request is larger than 4 KiB")
		case errors.Is(err, io.EOF):
			writeError(w, apierr.InvalidBody, "the request needs a JSON body")
		default:
			writeError(w, apierr.InvalidBody, "invalid JSON: "+strings.TrimPrefix(err.Error(), "json: "))
		}
		return false
	}
	return true
}

func (r *Relay) clientKey(req *http.Request) string {
	return auth.ThrottleKey(r.Proxies.ClientIP(req))
}

// --- small endpoints ---------------------------------------------------------

func (r *Relay) healthz(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

func (r *Relay) readyz(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	if err := r.Store.Ping(ctx); err != nil {
		r.Log.Warn("not ready", "error", err)
		writeError(w, apierr.Unavailable, "the database is not available")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

// config is GET /v1/config: which apps this relay can wake, so that an app can
// tell whether it is the relay for it.
func (r *Relay) config(w http.ResponseWriter, req *http.Request) {
	d := r.Senders.Describe()
	apps := d.Apps
	if apps == nil {
		apps = map[string][]string{}
	}
	writeJSON(w, http.StatusOK, struct {
		APIVersion int                 `json:"api_version"`
		Role       string              `json:"role"`
		Apps       map[string][]string `json:"apps"`
	}{apiVersion, "push-relay", apps})
}

// --- handles -----------------------------------------------------------------

func newHandle() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func newHandleSecret() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return HandleSecretPrefix + base64.RawURLEncoding.EncodeToString(b[:])
}

// register is POST /v1/handles: an app gives the relay its push token and gets a
// handle to give to servers, and a secret to remove it with.
func (r *Relay) register(w http.ResponseWriter, req *http.Request) {
	if ok, wait := r.registerIP.Allow(r.clientKey(req)); !ok {
		tooMany(w, wait, "registrations")
		return
	}
	var in struct {
		AppID    string `json:"app_id"`
		Platform string `json:"platform"`
		Provider string `json:"provider"`
		Token    string `json:"token"`
	}
	if !readJSON(w, req, &in, true) {
		return
	}
	provider := push.Provider(in.Provider)
	if provider != push.FCM && provider != push.APNs {
		writeError(w, apierr.InvalidParameter, "provider: must be fcm or apns")
		return
	}
	if !push.ValidAppID(in.AppID) {
		writeError(w, apierr.InvalidParameter, "app_id: required: the app's identifier, such as org.example.flare")
		return
	}
	if !contains(push.Platforms, in.Platform) {
		writeError(w, apierr.InvalidParameter, "platform: must be one of "+strings.Join(push.Platforms, ", "))
		return
	}
	if _, ok := r.Senders.For(push.Device{Provider: provider, AppID: in.AppID}); !ok {
		writeError(w, apierr.Unsupported, fmt.Sprintf("this relay has no %s credentials for the app %q", strings.ToUpper(in.Provider), in.AppID))
		return
	}
	token, err := push.CheckToken(provider, in.Token)
	if err != nil {
		writeError(w, apierr.InvalidParameter, err.Error())
		return
	}

	handle, secret := newHandle(), newHandleSecret()
	err = r.Store.Register(req.Context(), Handle{
		Hash: push.HashSecret(handle), SecretHash: push.HashSecret(secret),
		AppID: in.AppID, Platform: in.Platform, Provider: provider, Token: token, CreatedAt: r.Now(),
	})
	if err != nil {
		r.Log.Error("could not register a handle", "error", err)
		writeError(w, apierr.Internal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Handle string `json:"handle"`
		Secret string `json:"secret"`
	}{handle, secret})
}

// remove is DELETE /v1/handles/{handle}: the app revokes a handle, so that no
// server can wake it any more.
func (r *Relay) remove(w http.ResponseWriter, req *http.Request) {
	secret := req.Header.Get(HandleSecretHeader)
	if secret == "" {
		writeError(w, apierr.Unauthorized, "send the secret that registering the handle returned in the "+HandleSecretHeader+" header")
		return
	}
	hash := push.HashSecret(req.PathValue("handle"))
	h, err := r.Store.Find(req.Context(), hash)
	if errors.Is(err, ErrNotFound) {
		writeError(w, apierr.NotFound, "there is no such handle")
		return
	}
	if err != nil {
		r.Log.Error("could not read a handle", "error", err)
		writeError(w, apierr.Internal, "internal error")
		return
	}
	if subtle.ConstantTimeCompare([]byte(push.HashSecret(secret)), []byte(h.SecretHash)) != 1 {
		writeError(w, apierr.InvalidCredentials, "the "+HandleSecretHeader+" is not this handle's")
		return
	}
	if err := r.Store.Remove(req.Context(), hash); err != nil && !errors.Is(err, ErrNotFound) {
		r.Log.Error("could not remove a handle", "error", err)
		writeError(w, apierr.Internal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- waking ------------------------------------------------------------------

var idRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// wake is POST /v1/wake: a server asks the relay to tell a device that a message
// is waiting.
func (r *Relay) wake(w http.ResponseWriter, req *http.Request) {
	if ok, wait := r.wakeIP.Allow(r.clientKey(req)); !ok {
		tooMany(w, wait, "requests from this address")
		return
	}
	var in push.WakeRequest
	// Not strict: servers and relays are upgraded separately, and a newer server
	// may say more than this relay understands.
	if !readJSON(w, req, &in, false) {
		return
	}
	switch {
	case !push.ValidHandle(in.Handle):
		writeError(w, apierr.InvalidParameter, "handle: not a handle")
		return
	case !idRE.MatchString(in.Sub):
		writeError(w, apierr.InvalidParameter, "sub: required")
		return
	case !idRE.MatchString(in.ID):
		writeError(w, apierr.InvalidParameter, "id: required")
		return
	case in.Priority < 0 || in.Priority > 5:
		writeError(w, apierr.InvalidParameter, "priority: must be between 1 and 5")
		return
	case in.TTL < 0:
		writeError(w, apierr.InvalidParameter, "ttl: must not be negative")
		return
	}

	hash := push.HashSecret(in.Handle)
	h, err := r.Store.Find(req.Context(), hash)
	if errors.Is(err, ErrNotFound) {
		// The server forgets the device on this answer: the app has to register again.
		writeError(w, apierr.NotFound, "there is no such handle")
		return
	}
	if err != nil {
		r.Log.Error("could not read a handle", "error", err)
		writeError(w, apierr.Internal, "internal error")
		return
	}
	if ok, wait := r.wakeHandle.Allow(hash); !ok {
		tooMany(w, wait, "wake-ups for this device")
		return
	}

	device := push.Device{Provider: h.Provider, AppID: h.AppID, Platform: h.Platform, Target: h.Token}
	sender, ok := r.Senders.For(device)
	if !ok {
		// The operator removed this app's credentials after it registered.
		writeError(w, apierr.Undeliverable, "this relay can no longer wake this app")
		return
	}
	priority := in.Priority
	if priority == 0 {
		priority = message.DefaultPriority
	}
	ttl := time.Duration(in.TTL) * time.Second
	if ttl == 0 {
		ttl = defaultTTL
	}
	ttl = min(ttl, push.MaxWakeTTL)
	now := r.Now()

	ctx, cancel := context.WithTimeout(req.Context(), sendTimeout)
	defer cancel()
	err = sender.Send(ctx, push.Notification{
		Device: device, SubscriptionID: in.Sub, Message: message.Message{ID: in.ID, Priority: priority},
		Payload: push.WakePayload(in.Sub, in.ID), WakeUp: true, Expires: now.Add(ttl), Now: now,
	})
	r.answer(w, req, h, err, now)
}

// answer tells the server how the push service took it.
func (r *Relay) answer(w http.ResponseWriter, req *http.Request, h Handle, err error, now time.Time) {
	var se *push.SendError
	switch {
	case err == nil:
		_ = r.Store.Woke(req.Context(), h.Hash, now)
		w.WriteHeader(http.StatusNoContent)
	case errors.As(err, &se) && se.Unregistered:
		// The push service no longer knows the device (the app was removed): forget
		// the handle, and tell the server to forget the device.
		_ = r.Store.Remove(req.Context(), h.Hash)
		writeError(w, apierr.NotFound, "the device can no longer be reached")
	case errors.As(err, &se) && se.Permanent:
		r.Log.Warn("a push service refused a wake-up for good", "app", h.AppID, "provider", string(h.Provider), "error", err)
		writeError(w, apierr.Undeliverable, "the push service refused the notification")
	default:
		wait := time.Duration(0)
		if errors.As(err, &se) {
			wait = se.RetryAfter
		}
		r.Log.Warn("a push service did not take a wake-up", "app", h.AppID, "provider", string(h.Provider), "error", err)
		if wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		}
		writeError(w, apierr.Unavailable, "the push service did not take the notification; try again later")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
