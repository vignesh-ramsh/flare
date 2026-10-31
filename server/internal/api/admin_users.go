package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
)

const maxAdminBody = 16 << 10

type userOut struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
	Created  int64  `json:"created"`
}

func describeUser(u auth.User) userOut {
	return userOut{Name: u.Name, Role: string(u.Role), Disabled: u.Disabled, Created: u.CreatedAt.Unix()}
}

// readJSON reads a request body into v, answering the request if it can't. Unknown fields
// are refused, so a misspelled one is never silently ignored.
func (a *API) readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			writeError(w, apierr.TooLarge, "the request is too large")
		case errors.Is(err, io.EOF):
			writeError(w, apierr.InvalidBody, "send the request as JSON")
		default:
			writeError(w, apierr.InvalidBody, "invalid JSON: "+err.Error())
		}
		return false
	}
	return true
}

// accountFailed answers a request whose change to an account was refused.
func (a *API) accountFailed(w http.ResponseWriter, err error, what string) {
	var input *auth.InputError
	switch {
	case errors.As(err, &input):
		writeError(w, apierr.InvalidParameter, input.Msg)
	case errors.Is(err, auth.ErrExists):
		writeError(w, apierr.AlreadyExists, err.Error())
	case errors.Is(err, auth.ErrNotFound):
		writeError(w, apierr.NotFound, err.Error())
	default:
		a.Log.Error("could not "+what, "error", err)
		writeError(w, apierr.Internal, "internal error")
	}
}

// listUsers is GET /v1/admin/users.
func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.identifyAdmin(w, r); !ok {
		return
	}
	users, err := a.Auth.Users(r.Context())
	if err != nil {
		a.accountFailed(w, err, "list users")
		return
	}
	out := make([]userOut, len(users))
	for i, u := range users {
		out[i] = describeUser(u)
	}
	writeJSON(w, http.StatusOK, struct {
		Users []userOut `json:"users"`
	}{out})
}

// createUser is POST /v1/admin/users: {"name", "password", "role"}; the role defaults to user.
func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	admin, ok := a.identifyAdmin(w, r)
	if !ok {
		return
	}
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !a.readJSON(w, r, &in) {
		return
	}
	if in.Role == "" {
		in.Role = string(auth.RoleUser)
	}
	u, err := a.Auth.CreateUser(r.Context(), in.Name, in.Password, auth.Role(in.Role))
	if err != nil {
		a.accountFailed(w, err, "create a user")
		return
	}
	a.Log.Info("user created", "name", u.Name, "role", u.Role, "by", admin.User.Name)
	writeJSON(w, http.StatusCreated, describeUser(u))
}

// changeUser is PATCH /v1/admin/users/{name}: any of "role", "password" and "disabled".
//
// An administrator may not demote, disable or delete themselves: that would leave them
// locked out of the very place they are working in, and if they were the only one, no one
// able to undo it.
func (a *API) changeUser(w http.ResponseWriter, r *http.Request) {
	admin, ok := a.identifyAdmin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	var in struct {
		Role     *string `json:"role"`
		Password *string `json:"password"`
		Disabled *bool   `json:"disabled"`
	}
	if !a.readJSON(w, r, &in) {
		return
	}
	if in.Role == nil && in.Password == nil && in.Disabled == nil {
		writeError(w, apierr.InvalidBody, `send at least one of "role", "password" and "disabled"`)
		return
	}
	self := admin.User.Name == name
	if self && ((in.Disabled != nil && *in.Disabled) || (in.Role != nil && auth.Role(*in.Role) != auth.RoleAdmin)) {
		writeError(w, apierr.Conflict, "you cannot demote or disable yourself: ask another administrator")
		return
	}
	// Everything is checked before anything changes, so a refused request changes nothing.
	if _, err := a.Auth.FindUser(r.Context(), name); err != nil {
		a.accountFailed(w, err, "find a user")
		return
	}
	if in.Role != nil {
		if _, err := auth.ParseRole(*in.Role); err != nil {
			writeError(w, apierr.InvalidParameter, err.Error())
			return
		}
	}
	if in.Password != nil {
		if err := auth.ValidPassword(*in.Password); err != nil {
			writeError(w, apierr.InvalidParameter, err.Error())
			return
		}
	}
	if in.Password != nil {
		if err := a.Auth.SetPassword(r.Context(), name, *in.Password); err != nil {
			a.accountFailed(w, err, "change a password")
			return
		}
	}
	if in.Role != nil {
		if err := a.Auth.SetRole(r.Context(), name, auth.Role(*in.Role)); err != nil {
			a.accountFailed(w, err, "change a role")
			return
		}
	}
	if in.Disabled != nil {
		if err := a.Auth.SetDisabled(r.Context(), name, *in.Disabled); err != nil {
			a.accountFailed(w, err, "disable or enable a user")
			return
		}
	}
	updated, err := a.Auth.FindUser(r.Context(), name)
	if err != nil {
		a.accountFailed(w, err, "find a user")
		return
	}
	a.Log.Info("user changed", "name", name, "by", admin.User.Name,
		"password", in.Password != nil, "role", in.Role != nil, "disabled", in.Disabled != nil)
	writeJSON(w, http.StatusOK, describeUser(updated))
}

// deleteUser is DELETE /v1/admin/users/{name}, with their tokens and access rules.
func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	admin, ok := a.identifyAdmin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if admin.User.Name == name {
		writeError(w, apierr.Conflict, "you cannot delete yourself: ask another administrator")
		return
	}
	if err := a.Auth.DeleteUser(r.Context(), name); err != nil {
		a.accountFailed(w, err, "delete a user")
		return
	}
	a.Log.Info("user deleted", "name", name, "by", admin.User.Name)
	w.WriteHeader(http.StatusNoContent)
}

// userTokens is GET /v1/admin/users/{name}/tokens: their tokens, without the secrets, which
// are not kept.
func (a *API) userTokens(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.identifyAdmin(w, r); !ok {
		return
	}
	user, err := a.Auth.FindUser(r.Context(), r.PathValue("name"))
	if err != nil {
		a.accountFailed(w, err, "find a user")
		return
	}
	tokens, err := a.Auth.Tokens(r.Context(), user.ID)
	if err != nil {
		a.accountFailed(w, err, "list tokens")
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

// revokeUserToken is DELETE /v1/admin/users/{name}/tokens/{id}. Whatever uses the token is cut
// off, including connections that are open, within the interval at which they re-check.
func (a *API) revokeUserToken(w http.ResponseWriter, r *http.Request) {
	admin, ok := a.identifyAdmin(w, r)
	if !ok {
		return
	}
	user, err := a.Auth.FindUser(r.Context(), r.PathValue("name"))
	if err != nil {
		a.accountFailed(w, err, "find a user")
		return
	}
	if err := a.Auth.DeleteToken(r.Context(), r.PathValue("id"), user.ID); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, apierr.NotFound, "there is no such token")
			return
		}
		a.accountFailed(w, err, "revoke a token")
		return
	}
	a.Log.Info("token revoked", "user", user.Name, "token", r.PathValue("id"), "by", admin.User.Name)
	w.WriteHeader(http.StatusNoContent)
}

type ruleOut struct {
	Who     string `json:"who"`
	Pattern string `json:"pattern"`
	Access  string `json:"access"`
}

// listAccess is GET /v1/admin/access: every access rule. "who" is a user name, or "everyone".
func (a *API) listAccess(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.identifyAdmin(w, r); !ok {
		return
	}
	rules, err := a.Auth.AllRules(r.Context())
	if err != nil {
		a.accountFailed(w, err, "list access rules")
		return
	}
	out := make([]ruleOut, len(rules))
	for i, rule := range rules {
		out[i] = ruleOut{rule.Who, rule.Pattern, string(rule.Access)}
	}
	writeJSON(w, http.StatusOK, struct {
		Rules   []ruleOut `json:"rules"`
		Default string    `json:"default_access"`
	}{out, string(a.Auth.Default())})
}

// grantAccess is PUT /v1/admin/access: {"who", "pattern", "access"}. A rule for the same user
// and pattern is replaced.
func (a *API) grantAccess(w http.ResponseWriter, r *http.Request) {
	admin, ok := a.identifyAdmin(w, r)
	if !ok {
		return
	}
	var in ruleOut
	if !a.readJSON(w, r, &in) {
		return
	}
	if err := a.Auth.Grant(r.Context(), in.Who, in.Pattern, auth.Access(in.Access)); err != nil {
		a.accountFailed(w, err, "grant access")
		return
	}
	a.Log.Info("access granted", "who", in.Who, "pattern", in.Pattern, "access", in.Access, "by", admin.User.Name)
	writeJSON(w, http.StatusOK, in)
}

// revokeAccess is DELETE /v1/admin/access?who=...&pattern=...
func (a *API) revokeAccess(w http.ResponseWriter, r *http.Request) {
	admin, ok := a.identifyAdmin(w, r)
	if !ok {
		return
	}
	who, pattern := r.URL.Query().Get("who"), r.URL.Query().Get("pattern")
	if who == "" || pattern == "" {
		writeError(w, apierr.InvalidParameter, `give the rule's "who" and "pattern" as query parameters`)
		return
	}
	if err := a.Auth.Revoke(r.Context(), who, pattern); err != nil {
		a.accountFailed(w, err, "revoke access")
		return
	}
	a.Log.Info("access revoked", "who", who, "pattern", pattern, "by", admin.User.Name)
	w.WriteHeader(http.StatusNoContent)
}
