package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
)

// recorder remembers the status of a response and whether it has started. It
// passes everything else, including flushing and hijacking, through to the real
// writer by way of Unwrap, which http.ResponseController and the WebSocket
// library both follow.
type recorder struct {
	http.ResponseWriter
	status  int
	started bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.started {
		r.status, r.started = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.started {
		r.status, r.started = http.StatusOK, true
	}
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (a *API) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// recoverPanics keeps a bug in one handler from taking the server down.
func (a *API) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{ResponseWriter: w}
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				a.Log.Error("handler panicked", "panic", p, "route", r.Pattern)
				if !rec.started {
					writeError(rec, apierr.Internal, "internal error")
				}
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

// logRequests writes one line per request, when it finishes. For a stream that
// is when the client disconnects, so the duration is how long it was connected.
//
// It logs the route that matched (such as "POST /{topic}") rather than the path,
// and no client address: topic names act like passwords on an open server, and
// who connected is not something a notification server needs to keep.
func (a *API) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			status = http.StatusSwitchingProtocols // the library wrote it past the recorder
		}
		level := slog.LevelInfo
		switch {
		case r.Pattern == "GET /healthz" || r.Pattern == "GET /readyz":
			level = slog.LevelDebug // load balancers poll these constantly
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}
		a.Log.Log(r.Context(), level, "request",
			"method", r.Method, "route", r.Pattern, "status", status, "duration", time.Since(start).Round(time.Millisecond).String())
	})
}
