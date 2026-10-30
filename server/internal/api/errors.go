package api

import (
	"encoding/json"
	"net/http"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
)

// errorBody is the JSON every error response has.
type errorBody struct {
	Code  int    `json:"code"`
	HTTP  int    `json:"http"`
	Error string `json:"error"`
}

// writeError answers with the HTTP status that goes with code and a JSON body.
func writeError(w http.ResponseWriter, code int, text string) {
	status := apierr.HTTPStatus(code)
	writeJSON(w, status, errorBody{Code: code, HTTP: status, Error: text})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
