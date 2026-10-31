package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/push"
	"github.com/vignesh-ramsh/flare/server/internal/store"
)

// deviceSecretHeader carries the secret a device was given when it registered.
// It is not an Authorization header: a device acts for its owner by holding the
// secret, whatever the owner has logged in as, and a user's token is checked
// against the throttle for wrong passwords while this is not.
const deviceSecretHeader = "Device-Secret"

// maxDeviceRequest is the largest body of a device request. A Web Push
// registration with its keys is under a kilobyte.
const maxDeviceRequest = 16 << 10

// pushEnabled answers the request if the server has no push notifications.
func (a *API) pushEnabled(w http.ResponseWriter) bool {
	if a.Push == nil {
		writeError(w, apierr.NotFound, "this server does not send push notifications")
		return false
	}
	return true
}

// decodeBody reads a small JSON object, refusing unknown fields so that a typo
// (or a field from a newer API) is an error instead of being ignored.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout))
	defer func() { _ = rc.SetReadDeadline(time.Time{}) }()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDeviceRequest))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.More() {
		err = errors.New("unexpected data after the JSON object")
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			writeError(w, apierr.TooLarge, "the request is larger than 16 KiB")
		case errors.Is(err, io.EOF):
			writeError(w, apierr.InvalidBody, "the request needs a JSON body")
		default:
			writeError(w, apierr.InvalidBody, "invalid JSON: "+strings.TrimPrefix(err.Error(), "json: "))
		}
		return false
	}
	return true
}

// registerDevice is POST /v1/devices: an app says how it can be woken, and gets
// an ID and a secret for managing that.
func (a *API) registerDevice(w http.ResponseWriter, r *http.Request) {
	if !a.pushEnabled(w) {
		return
	}
	id, ok := a.identify(w, r)
	if !ok {
		return
	}
	// A device acts with its owner's access. Without an owner it would have only
	// what an anonymous visitor has, which on a server that lets nobody in is
	// nothing, so there is no point accepting it.
	if id.Anonymous() && !a.Auth.Default().Allows(auth.Read) {
		a.authFailed(w, fmt.Errorf("%w: log in to register a device; this server does not let anonymous visitors read", auth.ErrUnauthorized))
		return
	}

	var in struct {
		AppID      string `json:"app_id"`
		Platform   string `json:"platform"`
		Provider   string `json:"provider"`
		Token      string `json:"token"`
		Endpoint   string `json:"endpoint"`
		AppVersion string `json:"app_version"`
		Keys       struct {
			P256DH string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	d, secret, err := a.Push.Register(r.Context(), id.UserID(), push.Registration{
		AppID: in.AppID, Platform: in.Platform, Provider: push.Provider(in.Provider),
		Token: in.Token, Endpoint: in.Endpoint, AppVersion: in.AppVersion,
		Keys: push.Keys{P256DH: in.Keys.P256DH, Auth: in.Keys.Auth},
	})
	var input *push.InputError
	switch {
	case errors.As(err, &input):
		writeError(w, apierr.InvalidParameter, input.Error())
	case errors.Is(err, push.ErrUnsupported):
		writeError(w, apierr.Unsupported, err.Error())
	case errors.Is(err, push.ErrTooManyDevices):
		writeError(w, apierr.TooManyDevices, "too many devices are registered; remove one first")
	case err != nil:
		a.Log.Error("could not register a device", "error", err)
		writeError(w, apierr.Internal, "internal error")
	default:
		writeJSON(w, http.StatusCreated, struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		}{d.ID, secret})
	}
}

// deviceSecret reads the secret from the request, answering it if there is none.
func deviceSecret(w http.ResponseWriter, r *http.Request) (string, bool) {
	secret := r.Header.Get(deviceSecretHeader)
	if secret == "" {
		writeError(w, apierr.Unauthorized, "send the device's secret, which registering it returned, in the "+deviceSecretHeader+" header")
		return "", false
	}
	return secret, true
}

// deviceFailed answers a request about a device that the push service refused.
func (a *API) deviceFailed(w http.ResponseWriter, err error) {
	var input *push.InputError
	var forbidden *push.ForbiddenError
	switch {
	case errors.Is(err, push.ErrNotFound):
		writeError(w, apierr.NotFound, "there is no such device; it may have been removed, in which case register it again")
	case errors.Is(err, push.ErrBadSecret):
		writeError(w, apierr.InvalidCredentials, "the "+deviceSecretHeader+" is not this device's")
	case errors.As(err, &input):
		writeError(w, apierr.InvalidParameter, input.Error())
	case errors.As(err, &forbidden):
		writeError(w, apierr.Forbidden, forbidden.Error())
	default:
		a.Log.Error("device request failed", "error", err)
		writeError(w, apierr.Internal, "internal error")
	}
}

// followTopics is PUT /v1/devices/{id}/subscriptions: the complete list of topics
// the device is to be woken for. Each gets an opaque ID, which the device finds in
// every notification and uses to tell which topic it is for.
func (a *API) followTopics(w http.ResponseWriter, r *http.Request) {
	if !a.pushEnabled(w) {
		return
	}
	secret, ok := deviceSecret(w, r)
	if !ok {
		return
	}
	var in struct {
		Topics []string `json:"topics"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	subs, err := a.Push.Follow(r.Context(), r.PathValue("id"), secret, in.Topics)
	if err != nil {
		a.deviceFailed(w, err)
		return
	}
	type entry struct {
		Topic string `json:"topic"`
		ID    string `json:"id"`
	}
	out := make([]entry, len(subs))
	for i, s := range subs {
		out[i] = entry{s.Topic, s.ID}
	}
	writeJSON(w, http.StatusOK, struct {
		Subscriptions []entry `json:"subscriptions"`
	}{out})
}

// removeDevice is DELETE /v1/devices/{id}.
func (a *API) removeDevice(w http.ResponseWriter, r *http.Request) {
	if !a.pushEnabled(w) {
		return
	}
	secret, ok := deviceSecret(w, r)
	if !ok {
		return
	}
	if err := a.Push.Remove(r.Context(), r.PathValue("id"), secret); err != nil {
		a.deviceFailed(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getMessage is GET /v1/messages/{id}: one message, which an app fetches after a
// notification told it that a message is waiting.
func (a *API) getMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identify(w, r)
	if !ok {
		return
	}
	m, err := a.Hub.Message(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, apierr.NotFound, "there is no such message; it may have expired")
		return
	case err != nil:
		a.Log.Error("could not read a message", "error", err)
		writeError(w, apierr.Internal, "internal error")
		return
	}
	if !a.allowed(w, r, id, m.Topic, auth.Read) {
		return
	}
	writeJSON(w, http.StatusOK, m)
}
