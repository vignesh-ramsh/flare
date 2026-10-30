// Package client talks to a Flare server: it publishes messages and follows
// topics, reconnecting and resuming on its own when the connection drops.
//
// It is the reference for how any client should follow a topic, and the
// command-line tool is built on it.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

// APIError is an error the server reported.
type APIError struct {
	HTTP    int
	Code    int // zero if the server's answer wasn't a Flare error
	Message string
}

func (e *APIError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("%s (error %d)", e.Message, e.Code)
	}
	return e.Message
}

// Client is a connection to one server. Create it with New.
type Client struct {
	base string

	// HTTP is used for all requests. It must not have an overall Timeout, which
	// would cut streams off.
	HTTP *http.Client

	// MinBackoff and MaxBackoff bound how long Subscribe waits before
	// reconnecting; the wait doubles with every failed attempt.
	MinBackoff, MaxBackoff time.Duration
	// IdleTimeout is how long a stream may be silent, keepalives included,
	// before it is treated as dead. The server sends a keepalive every 30
	// seconds.
	IdleTimeout time.Duration
	// OnRetry, if set, is told why Subscribe lost its connection and how long it
	// will wait before trying again.
	OnRetry func(err error, wait time.Duration)
}

// New returns a client for the server at baseURL, such as https://flare.example.com.
func New(baseURL string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid server address %q: use a full address such as https://flare.example.com", baseURL)
	}
	return &Client{
		base:        strings.TrimSuffix(baseURL, "/"),
		HTTP:        &http.Client{},
		MinBackoff:  time.Second,
		MaxBackoff:  30 * time.Second,
		IdleTimeout: 90 * time.Second,
	}, nil
}

// PublishOptions are the optional parts of a message.
type PublishOptions struct {
	Title    string
	Priority string // 1-5 or a name such as "high"; empty for the default
	Tags     []string
	Click    string
}

// Publish sends body to topic and returns the message as the server stored it.
func (c *Client) Publish(ctx context.Context, topic, body string, opt PublishOptions) (message.Message, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+url.PathEscape(topic), strings.NewReader(body))
	if err != nil {
		return message.Message{}, err
	}
	set := func(name, value string) {
		if value != "" {
			// Non-ASCII text goes as an RFC 2047 encoded word, which every server and
			// proxy passes through unharmed.
			req.Header.Set(name, mime.QEncoding.Encode("utf-8", value))
		}
	}
	set("Title", opt.Title)
	set("Priority", opt.Priority)
	set("Tags", strings.Join(opt.Tags, ","))
	set("Click", opt.Click)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return message.Message{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return message.Message{}, readError(resp)
	}
	var m message.Message
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return message.Message{}, fmt.Errorf("unexpected answer from the server: %w", err)
	}
	return m, nil
}

// readError turns a non-200 response into an APIError.
func readError(resp *http.Response) *APIError {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var body struct {
		Code  int    `json:"code"`
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error != "" {
		return &APIError{HTTP: resp.StatusCode, Code: body.Code, Message: body.Error}
	}
	return &APIError{HTTP: resp.StatusCode, Message: fmt.Sprintf("the server answered %s", resp.Status)}
}

// SubscribeOptions say where to start and whether to keep going.
type SubscribeOptions struct {
	// Since is where to start: all, a message ID, a duration such as 10m, or a
	// unix timestamp. Empty means only new messages. See the API documentation.
	Since string
	// Poll fetches the cached messages once and returns, instead of following.
	Poll bool
}

// errClosed is how a followed connection ends when the server hangs up. For a
// poll that is the normal end of the response.
var errClosed = errors.New("the server closed the connection")

// handlerError marks an error returned by the caller's handler, which ends
// Subscribe instead of being retried.
type handlerError struct{ err error }

func (e handlerError) Error() string { return e.err.Error() }
func (e handlerError) Unwrap() error { return e.err }

// Subscribe calls handle for each message on topics, in order, each once.
//
// Unless opt.Poll is set it keeps going until ctx is cancelled (when it returns
// nil) or handle returns an error (which it returns). If the connection drops, it
// reconnects with growing delays and asks the server for everything after the
// last message it handled, so nothing is missed. It gives up only on an error
// that retrying can't fix, such as being refused access.
func (c *Client) Subscribe(ctx context.Context, topics []string, opt SubscribeOptions, handle func(message.Message) error) error {
	if len(topics) == 0 {
		return errors.New("no topic given")
	}
	path := "/" + strings.Join(topics, ",") + "/json"
	pos := &position{cursor: opt.Since}
	if opt.Poll {
		_, err := c.followOnce(ctx, path+"?poll=1", pos, false, handle)
		switch {
		case errors.Is(err, errClosed):
			return nil // a poll ends when the server has sent everything
		case errors.As(err, new(handlerError)):
			return errors.Unwrap(err)
		}
		return err
	}

	wait := c.MinBackoff
	for {
		connected, err := c.followOnce(ctx, path, pos, opt.Since == "", handle)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.As(err, new(handlerError)):
			return errors.Unwrap(err)
		case fatal(err):
			return err
		}
		if connected {
			wait = c.MinBackoff // it worked for a while: start the delays over
		}
		delay := jitter(wait)
		if c.OnRetry != nil {
			c.OnRetry(err, delay)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		wait = min(wait*2, c.MaxBackoff)
	}
}

// position is how far along a subscription is. It survives reconnects.
type position struct {
	// cursor is where the next connection starts: the `since` value to send.
	cursor string
	// handled is the last message given to the caller, or "" if none yet. The
	// server only sends messages after the cursor, so a repeat can only come from
	// resuming at a time boundary; this makes sure the caller never sees one.
	handled string
}

// fatal reports whether retrying can't help: the server understood the request
// and refused it.
func fatal(err error) bool {
	var api *APIError
	if !errors.As(err, &api) {
		return false
	}
	return api.HTTP >= 400 && api.HTTP < 500 && api.HTTP != http.StatusRequestTimeout && api.HTTP != http.StatusTooManyRequests
}

// jitter spreads d by up to 20% either way, so that many clients that lost the
// same server don't all come back at the same instant.
func jitter(d time.Duration) time.Duration {
	return d + time.Duration((rand.Float64()*0.4-0.2)*float64(d))
}

// followOnce makes one connection and reads it until it ends. connected says
// whether the server accepted the connection, however briefly. pos is kept
// current, so that the next connection can pick up where this one stopped.
//
// live means the caller asked for new messages only. Until a message arrives, the
// "open" event's ID is then the right place to resume from, so that messages
// published while reconnecting aren't lost.
func (c *Client) followOnce(ctx context.Context, path string, pos *position, live bool, handle func(message.Message) error) (connected bool, err error) {
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	target := c.base + path
	if pos.cursor != "" {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + "since=" + url.QueryEscape(pos.cursor)
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, readError(resp)
	}

	// A connection that goes quiet without closing, as happens behind some NATs
	// and proxies, would hang forever. The server sends keepalives, so silence
	// for much longer than that means the connection is dead.
	idle := time.AfterFunc(c.IdleTimeout, cancel)
	defer idle.Stop()

	dec := json.NewDecoder(resp.Body)
	for {
		var m message.Message
		if err := dec.Decode(&m); err != nil {
			if reqCtx.Err() != nil && ctx.Err() == nil {
				err = errors.New("the connection went silent")
			} else if errors.Is(err, io.EOF) {
				err = errClosed
			}
			return connected, err
		}
		idle.Reset(c.IdleTimeout)
		connected = true

		switch m.Event {
		case message.EventOpen:
			if live && pos.handled == "" {
				pos.cursor = m.ID
			}
		case message.EventMessage:
			if m.ID <= pos.handled {
				continue // a repeat
			}
			if err := handle(m); err != nil {
				return connected, handlerError{err}
			}
			pos.handled, pos.cursor = m.ID, m.ID
		}
	}
}
