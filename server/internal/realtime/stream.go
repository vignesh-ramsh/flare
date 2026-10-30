package realtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

// StreamOptions tune how streams are written. Zero values get defaults.
type StreamOptions struct {
	// Heartbeat is how often a quiet stream sends a keepalive event, so that
	// clients and proxies can tell it is alive.
	Heartbeat time.Duration
	// WriteTimeout is how long one write may take. A client that stops reading
	// would otherwise hold its connection, and its goroutine, forever.
	WriteTimeout time.Duration
}

// DefaultHeartbeat is how often keepalives are sent by default. Clients treat a
// stream that has been silent for much longer as dead.
const DefaultHeartbeat = 30 * time.Second

func (o *StreamOptions) fill() {
	if o.Heartbeat <= 0 {
		o.Heartbeat = DefaultHeartbeat
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 30 * time.Second
	}
}

// WriteError is returned by the streaming functions when writing to the client
// failed, which is what a client disconnecting looks like. It tells that routine
// event apart from a fault on the server's side.
type WriteError struct{ Err error }

func (e *WriteError) Error() string { return "write to client: " + e.Err.Error() }
func (e *WriteError) Unwrap() error { return e.Err }

// format is how an event is written to a stream.
type format interface {
	contentType() string
	write(w io.Writer, m message.Message) error
}

// jsonFormat writes one JSON object per line (newline-delimited JSON).
type jsonFormat struct{}

func (jsonFormat) contentType() string { return "application/x-ndjson; charset=utf-8" }
func (jsonFormat) write(w io.Writer, m message.Message) error {
	return json.NewEncoder(w).Encode(m)
}

// sseFormat writes Server-Sent Events. Messages carry their ID, so a browser's
// EventSource resumes from the last one by itself (Last-Event-ID). The "open"
// event deliberately has none: a reconnect before the backlog was delivered must
// not skip it.
type sseFormat struct{}

func (sseFormat) contentType() string { return "text/event-stream; charset=utf-8" }
func (sseFormat) write(w io.Writer, m message.Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	if m.Event == message.EventMessage {
		fmt.Fprintf(&b, "id: %s\n", m.ID)
	}
	fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", m.Event, data)
	_, err = w.Write(b.Bytes())
	return err
}

// ServeJSON streams messages on topics as newline-delimited JSON until the
// client goes away. See serveStream.
func ServeJSON(w http.ResponseWriter, r *http.Request, hub *Hub, topics []string, c Cursor, opt StreamOptions) error {
	return serveStream(w, r, hub, topics, c, opt, jsonFormat{})
}

// ServeSSE streams messages on topics as Server-Sent Events until the client
// goes away. See serveStream.
func ServeSSE(w http.ResponseWriter, r *http.Request, hub *Hub, topics []string, c Cursor, opt StreamOptions) error {
	return serveStream(w, r, hub, topics, c, opt, sseFormat{})
}

// serveStream writes an "open" event once the subscription is live, then the
// backlog and new messages as they come, with keepalives in the quiet
// stretches. The error it returns says why the stream ended; a client that went
// away or a server that is shutting down is not an error. A subscriber that fell
// behind gets broker.ErrLagged and should reconnect from its last message.
func serveStream(w http.ResponseWriter, r *http.Request, hub *Hub, topics []string, c Cursor, opt StreamOptions, f format) error {
	opt.fill()
	rc := http.NewResponseController(w)

	send := func(m message.Message) error {
		// Not every ResponseWriter supports deadlines or flushing (test
		// recorders don't); only a real failure should end the stream.
		if err := rc.SetWriteDeadline(time.Now().Add(opt.WriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return &WriteError{err}
		}
		if err := f.write(w, m); err != nil {
			return &WriteError{err}
		}
		if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return &WriteError{err}
		}
		return nil
	}

	h := w.Header()
	h.Set("Content-Type", f.contentType())
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // tell nginx not to hold the stream back
	w.WriteHeader(http.StatusOK)

	label := strings.Join(topics, ",")
	return hub.Follow(r.Context(), topics, c, opt.Heartbeat, Handler{
		Open: func() error {
			// The ID is a point to resume from for a client that asked for live
			// messages only and has received none yet.
			return send(message.Message{ID: hub.Checkpoint(), Time: hub.Now().Unix(), Event: message.EventOpen, Topic: label})
		},
		Message: send,
		Heartbeat: func() error {
			return send(message.Message{Time: hub.Now().Unix(), Event: message.EventKeepalive, Topic: label})
		},
	})
}

// ServePoll writes the cached messages on topics after the given ID as
// newline-delimited JSON and ends. started reports whether the response has
// begun; if it has not, the caller can still send an error response.
func ServePoll(w http.ResponseWriter, r *http.Request, hub *Hub, topics []string, after string, opt StreamOptions) (started bool, err error) {
	opt.fill()
	rc := http.NewResponseController(w)
	f := jsonFormat{}

	err = hub.Replay(r.Context(), topics, after, func(m message.Message) error {
		if !started {
			started = true
			h := w.Header()
			h.Set("Content-Type", f.contentType())
			h.Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
		}
		if err := rc.SetWriteDeadline(time.Now().Add(opt.WriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return &WriteError{err}
		}
		if err := f.write(w, m); err != nil {
			return &WriteError{err}
		}
		return nil
	})
	if err == nil && !started {
		started = true
		h := w.Header()
		h.Set("Content-Type", f.contentType())
		h.Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
	}
	return started, err
}
