package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/theme"
)

// maxThemeBody is the most a theme's JSON may be: 32 colours is about 1 KB, so this is
// generous and still far from a way to fill the disk.
const maxThemeBody = 16 << 10

// identifyAdmin is identifyUser for requests only an administrator may make.
func (a *API) identifyAdmin(w http.ResponseWriter, r *http.Request) (auth.Identity, bool) {
	id, ok := a.identifyUser(w, r)
	if !ok {
		return id, false
	}
	if id.User == nil || id.User.Role != auth.RoleAdmin {
		writeError(w, apierr.Forbidden, "this needs an administrator")
		return id, false
	}
	return id, true
}

// themeOut is a theme as the API shows it.
type themeOut struct {
	Name       string            `json:"name"`
	Brightness string            `json:"brightness"`
	Colors     map[string]string `json:"colors"`
	Created    int64             `json:"created,omitempty"`
	Updated    int64             `json:"updated,omitempty"`
}

func (a *API) themesOrUnavailable(w http.ResponseWriter) bool {
	if a.Themes == nil {
		writeError(w, apierr.Unsupported, "this server does not offer themes")
		return false
	}
	return true
}

// listThemes is GET /v1/themes: the themes this server offers, which apps show next to
// their own. A theme is only colours, so, like /v1/config, it reveals nothing an
// anonymous visitor shouldn't see, and it is public even on a server that keeps its
// topics private.
func (a *API) listThemes(w http.ResponseWriter, r *http.Request) {
	if !a.themesOrUnavailable(w) {
		return
	}
	stored, err := a.Themes.List(r.Context())
	if err != nil {
		a.Log.Error("could not list themes", "error", err)
		writeError(w, apierr.Internal, "internal error")
		return
	}
	out := make([]themeOut, len(stored))
	h := sha256.New()
	for i, t := range stored {
		out[i] = themeOut{Name: t.Name, Brightness: t.Brightness, Colors: t.Colors, Created: t.CreatedAt.Unix(), Updated: t.UpdatedAt.Unix()}
		fmt.Fprintf(h, "%s|%d;", t.Name, t.UpdatedAt.UnixNano())
	}
	// Apps ask again whenever they connect: let them be told nothing has changed.
	etag := `"` + hex.EncodeToString(h.Sum(nil)[:12]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Themes []themeOut `json:"themes"`
	}{out})
}

// themeSchema is GET /v1/themes/schema: the colours a theme can set, their groups and
// what each is for, and the rules themes must follow. The Admin UI builds its editor
// from it.
func (a *API) themeSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(theme.Contract())
}

// readTheme reads a theme from the request body, answering the request if it can't.
func (a *API) readTheme(w http.ResponseWriter, r *http.Request) (theme.Theme, bool) {
	// A theme fetched from the list has when it was added and changed; sending it back to edit
	// it is natural, so those two are read and ignored. Anything else unknown is an error.
	var body struct {
		theme.Theme
		Created json.RawMessage `json:"created"`
		Updated json.RawMessage `json:"updated"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxThemeBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, apierr.TooLarge, "the theme is too large")
		} else if errors.Is(err, io.EOF) {
			writeError(w, apierr.InvalidBody, "send the theme as JSON")
		} else {
			writeError(w, apierr.InvalidBody, "invalid JSON: "+err.Error())
		}
		return theme.Theme{}, false
	}
	in := body.Theme
	if in.Colors == nil {
		in.Colors = map[string]string{}
	}
	return in, true
}

// themeFailed answers a request whose theme the service refused.
func (a *API) themeFailed(w http.ResponseWriter, err error) {
	var input *theme.InputError
	switch {
	case errors.As(err, &input):
		writeError(w, apierr.InvalidParameter, input.Msg)
	case errors.Is(err, theme.ErrExists):
		writeError(w, apierr.AlreadyExists, err.Error())
	case errors.Is(err, theme.ErrLimit):
		writeError(w, apierr.LimitReached, "the server already holds "+strconv.Itoa(theme.MaxThemes)+" themes; delete one first")
	case errors.Is(err, theme.ErrNotFound):
		writeError(w, apierr.NotFound, err.Error())
	default:
		a.Log.Error("themes", "error", err)
		writeError(w, apierr.Internal, "internal error")
	}
}

// createTheme is POST /v1/admin/themes.
func (a *API) createTheme(w http.ResponseWriter, r *http.Request) {
	if !a.themesOrUnavailable(w) {
		return
	}
	if _, ok := a.identifyAdmin(w, r); !ok {
		return
	}
	in, ok := a.readTheme(w, r)
	if !ok {
		return
	}
	t, err := a.Themes.Create(r.Context(), in)
	if err != nil {
		a.themeFailed(w, err)
		return
	}
	a.Log.Info("theme added", "name", t.Name)
	writeJSON(w, http.StatusCreated, t)
}

// replaceTheme is PUT /v1/admin/themes/{name}: the theme in the body replaces the one
// with that name. The name can change only in its capital letters.
func (a *API) replaceTheme(w http.ResponseWriter, r *http.Request) {
	if !a.themesOrUnavailable(w) {
		return
	}
	if _, ok := a.identifyAdmin(w, r); !ok {
		return
	}
	in, ok := a.readTheme(w, r)
	if !ok {
		return
	}
	if theme.Key(in.Name) != theme.Key(r.PathValue("name")) {
		writeError(w, apierr.InvalidParameter, "a theme cannot be renamed: delete it and add another")
		return
	}
	t, err := a.Themes.Replace(r.Context(), in)
	if err != nil {
		a.themeFailed(w, err)
		return
	}
	a.Log.Info("theme changed", "name", t.Name)
	writeJSON(w, http.StatusOK, t)
}

// deleteTheme is DELETE /v1/admin/themes/{name}.
func (a *API) deleteTheme(w http.ResponseWriter, r *http.Request) {
	if !a.themesOrUnavailable(w) {
		return
	}
	if _, ok := a.identifyAdmin(w, r); !ok {
		return
	}
	if err := a.Themes.Delete(r.Context(), r.PathValue("name")); err != nil {
		a.themeFailed(w, err)
		return
	}
	a.Log.Info("theme deleted", "name", r.PathValue("name"))
	w.WriteHeader(http.StatusNoContent)
}
