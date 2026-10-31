package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/message"
	"github.com/vignesh-ramsh/flare/server/internal/realtime"
)

// Settings a publisher can send that this server does not support yet. They are
// refused instead of ignored: silently dropping "Delay" would send at once a
// message the publisher wanted held back.
var unsupportedSettings = []string{"Delay", "Actions", "Attach", "Filename", "Icon", "Markdown", "Cache"}

// bodyReadTimeout is how long a publisher has to send its message. Without it a
// client trickling bytes could hold a connection open indefinitely.
const bodyReadTimeout = 30 * time.Second

// maxJSONRequest is the largest JSON publish request accepted; the message
// itself is limited separately, by the configured message size.
const maxJSONRequest = 64 << 10

// checkTopic answers the request if topic can't be used, and reports whether it
// can.
func checkTopic(w http.ResponseWriter, topic string) bool {
	if !message.ValidTopic(topic) {
		writeError(w, apierr.InvalidTopic, "invalid topic name: use 1-64 letters, digits, - or _")
		return false
	}
	if message.IsReserved(topic) {
		writeError(w, apierr.InvalidTopic, "\""+topic+"\" is reserved and can't be used as a topic name")
		return false
	}
	return true
}

// setting reads a publish setting from its header, or else from the query
// parameter of the same name in lower case, for tools that can't set headers.
func setting(r *http.Request, header string) string {
	if v := r.Header.Get(header); v != "" {
		return v
	}
	return r.URL.Query().Get(strings.ToLower(header))
}

// unsupportedSetting returns the first unsupported setting the request uses.
func unsupportedSetting(r *http.Request) string {
	for _, name := range unsupportedSettings {
		if r.Header.Get(name) != "" || r.URL.Query().Has(strings.ToLower(name)) {
			return name
		}
	}
	return ""
}

// publishRaw is POST or PUT /{topic}: the body is the message, and everything
// else comes from headers, so that `curl -d hello host/topic` is a complete
// publish.
func (a *API) publishRaw(w http.ResponseWriter, r *http.Request) {
	topic := r.PathValue("topic")
	if !checkTopic(w, topic) {
		return
	}
	id, ok := a.identify(w, r)
	if !ok || !a.allowed(w, r, id, topic, auth.Write) {
		return
	}
	if name := unsupportedSetting(r); name != "" {
		writeError(w, apierr.Unsupported, name+" is not supported yet")
		return
	}

	d := message.Draft{Topic: topic}
	var err error
	if d.Title, err = decodeHeader(setting(r, "Title")); err != nil {
		writeError(w, apierr.InvalidParameter, "title: "+err.Error())
		return
	}
	if d.Priority, err = message.ParsePriority(setting(r, "Priority")); err != nil {
		writeError(w, apierr.InvalidParameter, err.Error())
		return
	}
	tags, err := decodeHeader(setting(r, "Tags"))
	if err != nil {
		writeError(w, apierr.InvalidParameter, "tags: "+err.Error())
		return
	}
	d.Tags = message.ParseTags(tags)
	d.Click = setting(r, "Click")

	// One byte more than the limit, so that a body over it is noticed and
	// refused with a precise answer by the hub rather than silently cut short.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout))
	limit := a.config().MessageSizeLimit
	r.Body = http.MaxBytesReader(w, r.Body, int64(limit)+1)
	body, err := io.ReadAll(r.Body)
	_ = rc.SetReadDeadline(time.Time{})
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, apierr.TooLarge, "message is larger than the limit of "+strconv.Itoa(limit)+" bytes")
		} else {
			writeError(w, apierr.InvalidBody, "could not read the request body")
		}
		return
	}
	d.Body = string(body)
	a.publish(w, r, d)
}

// publishJSON is POST /v1/messages: the same, with everything in a JSON body.
func (a *API) publishJSON(w http.ResponseWriter, r *http.Request) {
	// Who is asking is known from the headers alone, so find out before reading
	// a body for somebody who isn't allowed to send one.
	id, ok := a.identify(w, r)
	if !ok {
		return
	}
	var in struct {
		Topic    string   `json:"topic"`
		Message  string   `json:"message"`
		Title    string   `json:"title"`
		Priority int      `json:"priority"`
		Tags     []string `json:"tags"`
		Click    string   `json:"click"`
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout))
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONRequest))
	dec.DisallowUnknownFields()
	err := dec.Decode(&in)
	if err == nil && dec.More() {
		err = errors.New("unexpected data after the JSON object")
	}
	_ = rc.SetReadDeadline(time.Time{})
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, apierr.TooLarge, "request is larger than "+strconv.Itoa(maxJSONRequest)+" bytes")
		} else {
			writeError(w, apierr.InvalidBody, "invalid JSON: "+strings.TrimPrefix(err.Error(), "json: "))
		}
		return
	}

	if !checkTopic(w, in.Topic) || !a.allowed(w, r, id, in.Topic, auth.Write) {
		return
	}
	a.publish(w, r, message.Draft{
		Topic: in.Topic, Title: in.Title, Body: in.Message,
		Priority: in.Priority, Tags: in.Tags, Click: in.Click,
	})
}

// publish hands a draft to the hub and answers with the stored message.
func (a *API) publish(w http.ResponseWriter, r *http.Request, d message.Draft) {
	m, err := a.Hub.Publish(r.Context(), d)
	var problem *message.Problem
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, m)
	case errors.As(err, &problem) && problem.TooLarge:
		writeError(w, apierr.TooLarge, problem.Msg)
	case errors.As(err, &problem):
		writeError(w, realtime.ProblemCode(problem), problem.Msg)
	default:
		a.Log.Error("publish failed", "error", err)
		writeError(w, apierr.Internal, "could not store the message")
	}
}

// decodeHeader reads a header value that may use RFC 2047 encoding
// (=?UTF-8?B?...?=), which is how a client limited to ASCII headers sends
// non-ASCII text.
func decodeHeader(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(s)
	if err != nil {
		return "", errors.New("could not decode the value: " + err.Error())
	}
	return decoded, nil
}
