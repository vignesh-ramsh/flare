package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
)

// identify works out who the request comes from. If its credentials are wrong, or
// the sender has failed too often, it answers the request and reports false.
//
// A request with no credentials is an anonymous visitor, not an error. A request
// with credentials that don't work is refused: treating it as anonymous would let
// a client that sends a wrong token carry on as if nothing was wrong, and would
// hide the mistake.
func (a *API) identify(w http.ResponseWriter, r *http.Request) (auth.Identity, bool) {
	id, err := a.Auth.Authenticate(r)
	if err != nil {
		a.authFailed(w, err)
		return auth.Identity{}, false
	}
	return id, true
}

// identifyUser is identify for requests that need somebody to be logged in.
func (a *API) identifyUser(w http.ResponseWriter, r *http.Request) (auth.Identity, bool) {
	id, ok := a.identify(w, r)
	if ok && id.Anonymous() {
		a.authFailed(w, fmt.Errorf("%w: log in first", auth.ErrUnauthorized))
		return id, false
	}
	return id, ok
}

// allowed says whether id may do p to topic, answering the request if not.
func (a *API) allowed(w http.ResponseWriter, r *http.Request, id auth.Identity, topic string, p auth.Permission) bool {
	err := a.Auth.Authorize(r.Context(), id, topic, p)
	if err == nil {
		return true
	}
	a.authFailed(w, err)
	return false
}

// authFailed answers a request that authentication or authorization refused.
//
// 401 tells the client to log in (for an anonymous visitor who would be allowed if
// they did, and for credentials that don't work); 403 says that this person may
// not, whoever they log in as; 429 says to wait.
func (a *API) authFailed(w http.ResponseWriter, err error) {
	var throttled *auth.ThrottledError
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		challenge(w)
		writeError(w, apierr.Unauthorized, err.Error())
	case errors.Is(err, auth.ErrBadCredentials):
		challenge(w)
		writeError(w, apierr.InvalidCredentials, "invalid credentials: the user name and password, or the token, is wrong, expired or revoked")
	case errors.As(err, &throttled):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(throttled.RetryAfter.Seconds()))))
		writeError(w, apierr.Throttled, err.Error())
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, apierr.Forbidden, err.Error())
	default:
		a.Log.Error("authorization failed", "error", err)
		writeError(w, apierr.Internal, "internal error")
	}
}

// challenge adds the header a 401 must carry. It names Bearer only: browsers
// pop up their own password dialog for a Basic challenge, which would appear over
// any web app whose token had expired. Basic logins still work.
func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="flare"`)
}

// revalidator returns a function that checks, again, that id is still who it was
// and may still read topics. Streams call it at every heartbeat, so that revoking
// a token or taking away access ends a connection that is already open.
func (a *API) revalidator(r *http.Request, id auth.Identity, topics []string) func() error {
	current := id
	return func() error {
		fresh, err := a.Auth.Revalidate(r.Context(), current)
		if err != nil {
			return err
		}
		current = fresh
		for _, topic := range topics {
			if err := a.Auth.Authorize(r.Context(), current, topic, auth.Read); err != nil {
				return err
			}
		}
		return nil
	}
}
