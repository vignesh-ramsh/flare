package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/vignesh-ramsh/flare/server/internal/apierr"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/broker"
	"github.com/vignesh-ramsh/flare/server/internal/message"
)

// WSOptions configure ServeWS.
type WSOptions struct {
	// Identity is who the request that opened the connection came from. An "auth"
	// frame can replace it.
	Identity auth.Identity
	// Authorize says whether id may do perm to topic. It is asked for every
	// subscribe and publish, so a connection can only ever do what its owner is
	// allowed to.
	Authorize func(ctx context.Context, id auth.Identity, topic string, perm auth.Permission) error
	// Login handles an "auth" frame: it turns an access token into an identity.
	// Browsers can't set headers on a WebSocket, so this is how they log in. Nil
	// means the server has no accounts to log in to.
	Login func(ctx context.Context, token string) (auth.Identity, error)
	// Revalidate is called every PingInterval to check that the identity is still
	// good. If its credentials were revoked, the connection is closed; if access to
	// a topic was withdrawn, that subscription ends.
	Revalidate func(ctx context.Context, id auth.Identity) (auth.Identity, error)
	// MaxSubscriptions limits how many topics one connection can follow.
	MaxSubscriptions int
	// MaxMessageSize is the hub's message size limit, used to size the largest
	// frame a client may send.
	MaxMessageSize int
	// PingInterval is how often the server pings an idle client.
	PingInterval time.Duration
	// WriteTimeout is how long one write may take.
	WriteTimeout time.Duration
	Log          *slog.Logger
}

func (o *WSOptions) fill() {
	if o.MaxSubscriptions <= 0 {
		o.MaxSubscriptions = 100
	}
	if o.MaxMessageSize <= 0 {
		o.MaxMessageSize = 4096
	}
	if o.PingInterval <= 0 {
		o.PingInterval = 30 * time.Second
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 30 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
}

// wsRequest is a frame from the client. Which fields matter depends on Op.
type wsRequest struct {
	Op       string   `json:"op"`
	Topic    string   `json:"topic"`
	Since    string   `json:"since"`
	Message  string   `json:"message"`
	Title    string   `json:"title"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags"`
	Click    string   `json:"click"`
	Token    string   `json:"token"`
}

// wsEvent is a frame from the server other than a message.
type wsEvent struct {
	Event string `json:"event"`
	Topic string `json:"topic,omitempty"`
	ID    string `json:"id,omitempty"`
	User  string `json:"user,omitempty"`
	Code  int    `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// ServeWS runs the WebSocket protocol on one connection until it closes or the
// server shuts down. One connection can follow many topics and publish to any.
//
// The client sends JSON frames: {"op":"subscribe","topic":"t","since":"..."},
// {"op":"unsubscribe","topic":"t"} and {"op":"publish","topic":"t","message":"..."}
// (with optional title, priority, tags and click). The server answers with
// {"event":"subscribed"}, {"event":"unsubscribed"}, {"event":"published","id":...},
// {"event":"error","code":...,"error":"..."} and the messages themselves.
// A subscription that is dropped because the client fell behind is reported as
// an error with code 50002; the client should subscribe again from the last
// message it received.
func ServeWS(w http.ResponseWriter, r *http.Request, hub *Hub, opt WSOptions) error {
	opt.fill()
	// The default origin check applies: browsers may only connect from the page's
	// own origin. Clients that are not browsers send no Origin and are accepted.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err // Accept has already answered the request
	}
	conn.SetReadLimit(int64(opt.MaxMessageSize)*8 + 16*1024)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &wsConn{conn: conn, hub: hub, opt: opt, ctx: ctx, cancel: cancel, subs: map[string]*wsSub{}, who: opt.Identity}

	c.wg.Add(1)
	go c.pingLoop()

	// On server shutdown, close with a proper "going away" frame so clients know
	// to reconnect elsewhere or later. This can't be done by cancelling the
	// context of the blocked Read: the library then drops the connection without
	// a close frame. So Close is called from here, and the Read ends on its own.
	readDone := make(chan struct{})
	go func() {
		select {
		case <-r.Context().Done():
			conn.Close(websocket.StatusGoingAway, "server shutting down")
		case <-readDone:
		}
	}()

	c.readLoop()
	close(readDone)

	cancel()
	c.wg.Wait()
	conn.Close(websocket.StatusNormalClosure, "") // does nothing if already closed
	return nil
}

type wsSub struct{ cancel context.CancelFunc }

type wsConn struct {
	conn   *websocket.Conn
	hub    *Hub
	opt    WSOptions
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu   sync.Mutex
	subs map[string]*wsSub
	who  auth.Identity // who this connection is, guarded by mu
}

func (c *wsConn) identity() auth.Identity {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.who
}

// ioContext returns the context for one write or ping. It is deliberately not
// derived from the connection's own context.
//
// The library closes the TCP connection, without a close frame, if the context it
// is given is cancelled while a write or ping is under way. The connection's
// context is cancelled at shutdown, and a client can already have received a frame
// while the server's Write call for it has not quite returned; a shutdown in that
// moment would kill the connection instead of closing it politely. So writes get a
// timeout of their own, and shutdown is dealt with by closing the connection
// properly (see ServeWS).
func (c *wsConn) ioContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), c.opt.WriteTimeout)
}

// send writes v as one text frame. Writes are safe to make from several
// goroutines.
func (c *wsConn) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := c.ioContext()
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *wsConn) fail(topic string, code int, text string) {
	_ = c.send(wsEvent{Event: "error", Topic: topic, Code: code, Error: text})
}

func (c *wsConn) pingLoop() {
	defer c.wg.Done()
	t := time.NewTicker(c.opt.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			ctx, cancel := c.ioContext()
			err := c.conn.Ping(ctx)
			cancel()
			if err != nil {
				c.cancel() // the client is gone: stop reading and clean up
				c.conn.CloseNow()
				return
			}
			c.revalidate()
		}
	}
}

// revalidate checks that the connection's owner is still who they were and still
// allowed what they are subscribed to.
func (c *wsConn) revalidate() {
	if c.opt.Revalidate == nil {
		return
	}
	fresh, err := c.opt.Revalidate(c.ctx, c.identity())
	if err != nil {
		if errors.Is(err, auth.ErrBadCredentials) {
			c.fail("", apierr.InvalidCredentials, "your credentials are no longer valid")
			c.cancel()
			c.conn.Close(websocket.StatusPolicyViolation, "credentials no longer valid")
		}
		return // anything else is a hiccup on our side: try again next time
	}
	c.mu.Lock()
	c.who = fresh
	topics := make([]string, 0, len(c.subs))
	for topic := range c.subs {
		topics = append(topics, topic)
	}
	c.mu.Unlock()

	for _, topic := range topics {
		if err := c.opt.Authorize(c.ctx, fresh, topic, auth.Read); err != nil {
			c.mu.Lock()
			sub := c.subs[topic]
			delete(c.subs, topic)
			c.mu.Unlock()
			if sub != nil {
				sub.cancel()
				c.failAuth(topic, err)
			}
		}
	}
}

func (c *wsConn) readLoop() {
	for {
		// Not c.ctx: see ServeWS on why a Read must not be cancelled.
		typ, data, err := c.conn.Read(context.Background())
		if err != nil {
			return // closed, cancelled, or a frame over the size limit
		}
		if typ != websocket.MessageText {
			c.fail("", apierr.InvalidBody, "frames must be JSON text")
			continue
		}
		var req wsRequest
		if err := json.Unmarshal(data, &req); err != nil {
			c.fail("", apierr.InvalidBody, "frame is not valid JSON")
			continue
		}
		switch req.Op {
		case "subscribe":
			c.subscribe(req)
		case "unsubscribe":
			c.unsubscribe(req.Topic)
		case "publish":
			c.publish(req)
		case "auth":
			c.login(req)
		default:
			c.fail(req.Topic, apierr.InvalidParameter, "unknown op "+quote(req.Op)+": use subscribe, unsubscribe or publish")
		}
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// checkTopic validates a topic and asks the Authorizer; it reports any problem
// to the client and returns false.
func (c *wsConn) checkTopic(topic string, perm auth.Permission) bool {
	if !message.ValidTopic(topic) || message.IsReserved(topic) {
		c.fail(topic, apierr.InvalidTopic, "invalid topic name")
		return false
	}
	if err := c.opt.Authorize(c.ctx, c.identity(), topic, perm); err != nil {
		c.failAuth(topic, err)
		return false
	}
	return true
}

// failAuth reports why an operation was refused.
func (c *wsConn) failAuth(topic string, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		c.fail(topic, apierr.Unauthorized, err.Error())
	case errors.Is(err, auth.ErrForbidden):
		c.fail(topic, apierr.Forbidden, err.Error())
	default:
		c.opt.Log.Error("websocket authorization failed", "error", err)
		c.fail(topic, apierr.Internal, "internal error")
	}
}

// login handles an "auth" frame. It is only allowed before subscribing, so that
// a connection never holds subscriptions that were authorized as somebody else.
func (c *wsConn) login(req wsRequest) {
	if c.opt.Login == nil {
		c.fail("", apierr.Unauthorized, "this server has no accounts, so there is nothing to log in to")
		return
	}
	c.mu.Lock()
	busy := len(c.subs) > 0
	c.mu.Unlock()
	if busy {
		c.fail("", apierr.InvalidParameter, "log in before subscribing to anything")
		return
	}
	id, err := c.opt.Login(c.ctx, req.Token)
	var throttled *auth.ThrottledError
	switch {
	case err == nil:
		c.mu.Lock()
		c.who = id
		c.mu.Unlock()
		_ = c.send(wsEvent{Event: "authenticated", User: id.User.Name})
	case errors.Is(err, auth.ErrBadCredentials):
		c.fail("", apierr.InvalidCredentials, "that token is not valid")
	case errors.As(err, &throttled):
		c.fail("", apierr.Throttled, err.Error())
	default:
		c.opt.Log.Error("websocket login failed", "error", err)
		c.fail("", apierr.Internal, "internal error")
	}
}

func (c *wsConn) subscribe(req wsRequest) {
	if !c.checkTopic(req.Topic, auth.Read) {
		return
	}
	cursor := LiveCursor
	if req.Since != "" {
		var err error
		if cursor, err = ParseCursor(req.Since, c.hub.Now()); err != nil {
			c.fail(req.Topic, apierr.InvalidParameter, err.Error())
			return
		}
	}

	ctx, cancel := context.WithCancel(c.ctx)
	sub := &wsSub{cancel: cancel}
	c.mu.Lock()
	switch {
	case c.subs[req.Topic] != nil:
		c.mu.Unlock()
		cancel()
		c.fail(req.Topic, apierr.AlreadySubscribed, "already subscribed to this topic")
		return
	case len(c.subs) >= c.opt.MaxSubscriptions:
		c.mu.Unlock()
		cancel()
		c.fail(req.Topic, apierr.TooMany, "too many subscriptions on this connection")
		return
	}
	c.subs[req.Topic] = sub
	c.mu.Unlock()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer cancel()
		err := c.hub.Follow(ctx, []string{req.Topic}, cursor, 0, Handler{
			Open:    func() error { return c.send(wsEvent{Event: "subscribed", Topic: req.Topic}) },
			Message: func(m message.Message) error { return c.send(m) },
		})

		c.mu.Lock()
		if c.subs[req.Topic] == sub {
			delete(c.subs, req.Topic)
		}
		c.mu.Unlock()

		switch {
		case err == nil, errors.Is(err, context.Canceled):
		case errors.Is(err, broker.ErrLagged):
			c.fail(req.Topic, apierr.Lagged, "subscription dropped because the connection was too slow; subscribe again from the last message you received")
		case errors.Is(err, broker.ErrClosed):
		default:
			c.opt.Log.Error("websocket subscription failed", "error", err)
			c.fail(req.Topic, apierr.Internal, "internal error")
		}
	}()
}

func (c *wsConn) unsubscribe(topic string) {
	c.mu.Lock()
	sub := c.subs[topic]
	delete(c.subs, topic)
	c.mu.Unlock()
	if sub == nil {
		c.fail(topic, apierr.NotFound, "not subscribed to this topic")
		return
	}
	sub.cancel()
	_ = c.send(wsEvent{Event: "unsubscribed", Topic: topic})
}

func (c *wsConn) publish(req wsRequest) {
	if !c.checkTopic(req.Topic, auth.Write) {
		return
	}
	m, err := c.hub.Publish(c.ctx, message.Draft{
		Topic: req.Topic, Title: req.Title, Body: req.Message,
		Priority: req.Priority, Tags: req.Tags, Click: req.Click,
	})
	var problem *message.Problem
	switch {
	case err == nil:
		_ = c.send(wsEvent{Event: "published", Topic: m.Topic, ID: m.ID})
	case errors.As(err, &problem) && problem.TooLarge:
		c.fail(req.Topic, apierr.TooLarge, problem.Msg)
	case errors.As(err, &problem):
		c.fail(req.Topic, ProblemCode(problem), problem.Msg)
	default:
		c.opt.Log.Error("websocket publish failed", "error", err)
		c.fail(req.Topic, apierr.Internal, "internal error")
	}
}

// ProblemCode maps a publish that was refused to the error code to report.
func ProblemCode(p *message.Problem) int {
	if p.Field == "topic" {
		return apierr.InvalidTopic
	}
	return apierr.InvalidParameter
}
