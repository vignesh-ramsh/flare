package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/broker"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/realtime"
)

func (a *API) streamJSON(w http.ResponseWriter, r *http.Request) { a.stream(w, r, false) }
func (a *API) streamSSE(w http.ResponseWriter, r *http.Request)  { a.stream(w, r, true) }

// stream serves GET /{topics}/json and GET /{topics}/sse. Topics are a
// comma-separated list; the stream follows all of them.
//
// The query parameter `since` says where to start (see realtime.ParseCursor);
// without it a stream carries only new messages. `poll=1` on the JSON endpoint
// returns the cached messages and ends instead of waiting for more, and then
// `since` defaults to everything in the cache. Server-Sent Events also honour
// the Last-Event-ID header that browsers send when they reconnect.
func (a *API) stream(w http.ResponseWriter, r *http.Request, sse bool) {
	topics, err := message.ParseTopics(r.PathValue("topics"))
	if err != nil {
		writeError(w, apierr.InvalidTopic, err.Error())
		return
	}
	id, ok := a.identify(w, r)
	if !ok {
		return
	}
	for _, topic := range topics {
		if !a.allowed(w, r, id, topic, auth.Read) {
			return
		}
	}

	q := r.URL.Query()
	since := q.Get("since")
	if since == "" && sse {
		since = r.Header.Get("Last-Event-ID")
	}
	poll := !sse && isTrue(q.Get("poll"))

	cursor := realtime.LiveCursor
	if poll {
		cursor = realtime.AllCursor
	}
	if since != "" {
		if cursor, err = realtime.ParseCursor(since, a.Hub.Now()); err != nil {
			writeError(w, apierr.InvalidParameter, err.Error())
			return
		}
	}

	r, done := a.untilShutdown(r)
	defer done()
	// An open stream outlives the moment it was authorized, so it checks again at
	// every heartbeat; see revalidator.
	opts := a.Stream
	opts.Revalidate = a.revalidator(r, id, topics)
	switch {
	case poll:
		started, err := realtime.ServePoll(w, r, a.Hub, topics, cursor.After, a.Stream)
		a.streamEnded(w, err, started)
	case sse:
		a.streamEnded(w, realtime.ServeSSE(w, r, a.Hub, topics, cursor, opts), true)
	default:
		a.streamEnded(w, realtime.ServeJSON(w, r, a.Hub, topics, cursor, opts), true)
	}
}

// streamEnded deals with how a stream finished. Most endings are routine: the
// client left, the server is shutting down, or the client fell behind and will
// reconnect. Only a fault on the server's side is worth an error.
func (a *API) streamEnded(w http.ResponseWriter, err error, started bool) {
	var (
		writeErr *realtime.WriteError
		revoked  *realtime.RevokedError
	)
	switch {
	case err == nil, errors.Is(err, context.Canceled), errors.Is(err, broker.ErrClosed):
	case errors.Is(err, broker.ErrLagged):
		a.Log.Debug("stream ended: subscriber fell behind")
	case errors.As(err, &writeErr):
		a.Log.Debug("stream ended: client went away", "error", err)
	case errors.As(err, &revoked):
		a.Log.Debug("stream ended: access was withdrawn")
	default:
		a.Log.Error("stream failed", "error", err)
		if !started {
			writeError(w, apierr.Internal, "internal error")
		}
	}
}

func isTrue(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// websocket serves GET /v1/ws. Every subscribe and publish on the connection is
// authorized separately.
func (a *API) websocket(w http.ResponseWriter, r *http.Request) {
	// Native clients can log in with a header here; browsers, which can't set one,
	// send an "auth" frame afterwards.
	id, ok := a.identify(w, r)
	if !ok {
		return
	}
	ip := a.Auth.ClientIP(r)
	r, done := a.untilShutdown(r)
	defer done()
	err := realtime.ServeWS(w, r, a.Hub, realtime.WSOptions{
		Identity:  id,
		Authorize: a.Auth.Authorize,
		Login: func(ctx context.Context, token string) (auth.Identity, error) {
			return a.Auth.AuthenticateToken(ctx, token, ip)
		},
		Revalidate:     a.Auth.Revalidate,
		MaxMessageSize: a.Config.MessageSizeLimit,
		PingInterval:   a.WSPingInterval,
		WriteTimeout:   a.Stream.WriteTimeout,
		Log:            a.Log,
	})
	if err != nil {
		a.Log.Debug("websocket upgrade refused", "error", err) // the library has already answered
	}
}
