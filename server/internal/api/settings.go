package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/settings"
)

// config returns the server's configuration as it is now: the one it started with, with what
// an administrator has changed since laid over it.
func (a *API) config() config.Config {
	if a.Settings != nil {
		return a.Settings.Config()
	}
	return a.Config
}

func (a *API) settingsOrUnavailable(w http.ResponseWriter) bool {
	if a.Settings == nil {
		writeError(w, apierr.Unsupported, "this server's settings cannot be changed")
		return false
	}
	return true
}

// listSettings is GET /v1/admin/settings: the settings an administrator may change, with
// their values, where those come from, and which are waiting for a restart.
func (a *API) listSettings(w http.ResponseWriter, r *http.Request) {
	if !a.settingsOrUnavailable(w) {
		return
	}
	if _, ok := a.identifyAdmin(w, r); !ok {
		return
	}
	warnings := a.Settings.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, http.StatusOK, struct {
		Groups   []settings.Group `json:"groups"`
		Settings []settings.Item  `json:"settings"`
		Warnings []string         `json:"warnings"`
	}{settings.Groups(), a.Settings.List(), warnings})
}

// settingFailed answers a request whose setting could not be changed.
func (a *API) settingFailed(w http.ResponseWriter, err error) {
	var input *settings.InputError
	switch {
	case errors.As(err, &input):
		writeError(w, apierr.InvalidParameter, input.Msg)
	case errors.Is(err, settings.ErrUnknown):
		writeError(w, apierr.NotFound, err.Error())
	default:
		a.Log.Error("settings", "error", err)
		writeError(w, apierr.Internal, "internal error")
	}
}

// putSetting is PUT /v1/admin/settings/{key}: {"value": ...}.
func (a *API) putSetting(w http.ResponseWriter, r *http.Request) {
	if !a.settingsOrUnavailable(w) {
		return
	}
	admin, ok := a.identifyAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || len(body.Value) == 0 {
		if err == nil || errors.Is(err, io.EOF) {
			writeError(w, apierr.InvalidBody, `send {"value": ...}`)
		} else {
			writeError(w, apierr.InvalidBody, "invalid JSON: "+err.Error())
		}
		return
	}
	item, err := a.Settings.Set(r.Context(), r.PathValue("key"), body.Value)
	if err != nil {
		a.settingFailed(w, err)
		return
	}
	a.Log.Info("setting changed", "key", item.Key, "by", admin.User.Name, "restart_needed", item.PendingRestart)
	writeJSON(w, http.StatusOK, item)
}

// resetSetting is DELETE /v1/admin/settings/{key}: back to what the configuration says.
func (a *API) resetSetting(w http.ResponseWriter, r *http.Request) {
	if !a.settingsOrUnavailable(w) {
		return
	}
	admin, ok := a.identifyAdmin(w, r)
	if !ok {
		return
	}
	item, err := a.Settings.Reset(r.Context(), r.PathValue("key"))
	if err != nil {
		a.settingFailed(w, err)
		return
	}
	a.Log.Info("setting reset", "key", item.Key, "by", admin.User.Name)
	writeJSON(w, http.StatusOK, item)
}
