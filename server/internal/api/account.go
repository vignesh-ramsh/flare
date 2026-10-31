package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
)

// maxTokenLifetime is the longest a token can be asked to last: ten years, which
// also keeps the arithmetic far from overflowing.
const maxTokenLifetime = 10 * 365 * 24 * 60 * 60 // seconds

type tokenInfo struct {
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	Created  int64  `json:"created"`
	Expires  int64  `json:"expires,omitempty"`
	LastUsed int64  `json:"last_used,omitempty"`
}

func describe(t auth.Token) tokenInfo {
	info := tokenInfo{ID: t.ID, Label: t.Label, Created: t.CreatedAt.Unix()}
	if t.ExpiresAt != nil {
		info.Expires = t.ExpiresAt.Unix()
	}
	if t.LastUsedAt != nil {
		info.LastUsed = t.LastUsedAt.Unix()
	}
	return info
}

// login is POST /v1/auth/login: a user name and password (HTTP Basic) in, an
// access token out. Apps log in once and keep the token, so that the password is
// not stored on the device or sent with every request.
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	id, err := a.Auth.Login(r)
	if err != nil {
		a.authFailed(w, err)
		return
	}

	var in struct {
		Label     string `json:"label"`
		ExpiresIn int64  `json:"expires_in"` // seconds; 0 for never
	}
	if r.ContentLength != 0 { // the body is optional
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, apierr.InvalidBody, "invalid JSON: "+err.Error())
			return
		}
	}
	if in.ExpiresIn < 0 || in.ExpiresIn > maxTokenLifetime {
		writeError(w, apierr.InvalidParameter, "expires_in must be between 0 (never) and ten years, in seconds")
		return
	}

	secret, token, err := a.Auth.IssueToken(r.Context(), id.User.ID, in.Label, time.Duration(in.ExpiresIn)*time.Second)
	var input *auth.InputError
	switch {
	case errors.As(err, &input):
		writeError(w, apierr.InvalidParameter, input.Msg)
		return
	case errors.Is(err, auth.ErrTooManyTokens):
		writeError(w, apierr.TokenLimit, err.Error())
		return
	case err != nil:
		a.Log.Error("could not issue a token", "error", err)
		writeError(w, apierr.Internal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Token string `json:"token"`
		User  string `json:"user"`
		Role  string `json:"role"`
		tokenInfo
	}{secret, id.User.Name, string(id.User.Role), describe(token)})
}

// account is GET /v1/account: who the caller is and what they may do, which an app
// shows after logging in.
func (a *API) account(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identifyUser(w, r)
	if !ok {
		return
	}
	rules, err := a.Auth.Rules(r.Context(), id)
	if err != nil {
		a.Log.Error("could not read access rules", "error", err)
		writeError(w, apierr.Internal, "internal error")
		return
	}
	type entry struct {
		Pattern string `json:"pattern"`
		Access  string `json:"access"`
		Scope   string `json:"scope"` // "user" or "everyone"
	}
	access := make([]entry, 0, len(rules))
	for _, rule := range rules {
		scope := "user"
		if rule.UserID == "" {
			scope = "everyone"
		}
		access = append(access, entry{rule.Pattern, string(rule.Access), scope})
	}
	writeJSON(w, http.StatusOK, struct {
		User          string  `json:"user"`
		Role          string  `json:"role"`
		DefaultAccess string  `json:"default_access"`
		Access        []entry `json:"access"`
	}{id.User.Name, string(id.User.Role), string(a.Auth.Default()), access})
}

// listTokens is GET /v1/account/tokens.
func (a *API) listTokens(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identifyUser(w, r)
	if !ok {
		return
	}
	tokens, err := a.Auth.Tokens(r.Context(), id.User.ID)
	if err != nil {
		a.Log.Error("could not list tokens", "error", err)
		writeError(w, apierr.Internal, "internal error")
		return
	}
	out := make([]tokenInfo, len(tokens))
	for i, t := range tokens {
		out[i] = describe(t)
	}
	writeJSON(w, http.StatusOK, struct {
		Tokens []tokenInfo `json:"tokens"`
	}{out})
}

// deleteToken is DELETE /v1/account/tokens/{id}: revoke one of the caller's own
// tokens, including the one the request used, which logs that device out. A
// token that belongs to somebody else is reported as not found, so that its
// existence isn't revealed.
func (a *API) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identifyUser(w, r)
	if !ok {
		return
	}
	err := a.Auth.DeleteToken(r.Context(), r.PathValue("id"), id.User.ID)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		writeError(w, apierr.NotFound, "there is no such token")
	case err != nil:
		a.Log.Error("could not delete a token", "error", err)
		writeError(w, apierr.Internal, "internal error")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
