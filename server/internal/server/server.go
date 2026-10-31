// Package server wires the pieces together and runs them: it opens the store,
// serves the API, purges expired messages in the background, and shuts down
// cleanly.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/api"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/broker"
	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/push"
	"github.com/vignesh-ramsh/flare/server/internal/realtime"
	"github.com/vignesh-ramsh/flare/server/internal/store"
)

// Server is a running Flare node.
type Server struct {
	cfg    config.Config
	log    *slog.Logger
	store  *store.SQLite
	broker *broker.InProc
	hub    *realtime.Hub
	api    *api.API

	// dispatcher sends push notifications. Nil when push is off.
	dispatcher *push.Dispatcher

	// endStreams cancels shutdown, which tells streams and WebSockets to end.
	shutdown   context.Context
	endStreams context.CancelFunc

	purgeEvery time.Duration // how often expired messages are removed
}

// New opens the message store and builds a server. cfg must have been
// validated. Call Serve to run it, or Close to release the store if it is never
// run.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*Server, error) {
	path, err := cfg.DatabasePath()
	if err != nil {
		return nil, err
	}
	access, err := auth.ParseAccess(cfg.Auth.DefaultAccess)
	if err != nil {
		return nil, err
	}
	proxies, err := auth.ParseProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	st, err := store.OpenSQLite(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open the message store: %w", err)
	}

	authSvc := auth.NewService(st, auth.ServiceConfig{Default: access, Proxies: proxies})

	var pushSvc *push.Service
	var dispatcher *push.Dispatcher
	hubOpts := realtime.Options{TTL: cfg.CacheDuration, MaxMessageSize: cfg.MessageSizeLimit}
	if cfg.Push.Enabled {
		registry, err := buildRegistry(ctx, cfg, st, log)
		if err != nil {
			st.Close()
			return nil, err
		}
		pushSvc = push.NewService(st, authSvc, registry, push.ServiceConfig{AllowPrivateEndpoints: cfg.Push.AllowPrivateEndpoints})
		dispatcher = push.NewDispatcher(st, registry, authSvc, push.DispatcherConfig{ContentFree: cfg.Push.ContentFree, Log: log})
		hubOpts.Stored = dispatcher.Wake
		log.Info("push notifications are on", "services", registry.Names(), "content_free", cfg.Push.ContentFree)
	}

	br := broker.NewInProc()
	hub := realtime.NewHub(st, br, hubOpts)
	shutdown, endStreams := context.WithCancel(context.Background())
	return &Server{
		cfg: cfg, log: log, store: st, broker: br, hub: hub, dispatcher: dispatcher,
		api: api.New(api.Deps{
			Hub: hub, Config: cfg, Log: log, Shutdown: shutdown, Auth: authSvc, Push: pushSvc,
		}),
		shutdown: shutdown, endStreams: endStreams,
		purgeEvery: time.Minute,
	}, nil
}

// buildRegistry sets up the push services the configuration asks for. A credential
// that can't be read or used stops the server from starting, naming the file:
// better than a server that runs and quietly never notifies an app.
func buildRegistry(ctx context.Context, cfg config.Config, st *store.SQLite, log *slog.Logger) (*push.Registry, error) {
	p := cfg.Push
	registry := push.NewRegistry()
	endpoints := push.EndpointClient(p.AllowPrivateEndpoints)

	if p.UnifiedPush {
		registry.Add(push.UnifiedPush, push.NewUnifiedPush(endpoints, p.AllowPrivateEndpoints))
	}
	if p.WebPush {
		key, err := push.LoadVAPIDKey(ctx, st)
		if err != nil {
			return nil, fmt.Errorf("web push: %w", err)
		}
		subject := p.VAPIDSubject
		if subject == "" && strings.HasPrefix(cfg.BaseURL, "https://") {
			subject = cfg.BaseURL
		}
		if subject == "" {
			log.Warn("web push has no contact for push services to reach you at; " +
				"set push.vapid-subject or base-url (Apple's push service requires one)")
		}
		registry.Add(push.WebPush, push.NewWebPush(endpoints, key, subject, p.AllowPrivateEndpoints))
	}

	// FCM and APNs are fixed Google and Apple addresses, so they may use the
	// environment's proxy, unlike the endpoints users choose.
	google := push.NewHTTP2Client()
	for _, app := range p.Apps {
		if app.FCMCredentials != "" {
			creds, err := os.ReadFile(app.FCMCredentials)
			if err != nil {
				return nil, fmt.Errorf("push.apps %s: read fcm-credentials: %w", app.AppID, err)
			}
			sender, err := push.NewFCM(creds, google)
			if err != nil {
				return nil, fmt.Errorf("push.apps %s: fcm-credentials %s: %w", app.AppID, app.FCMCredentials, err)
			}
			registry.AddApp(push.FCM, app.AppID, sender)
		}
		if app.APNsKey != "" {
			key, err := os.ReadFile(app.APNsKey)
			if err != nil {
				return nil, fmt.Errorf("push.apps %s: read apns-key: %w", app.AppID, err)
			}
			sender, err := push.NewAPNs(key, app.APNsKeyID, app.APNsTeamID, app.AppID, app.APNsSandbox, google)
			if err != nil {
				return nil, fmt.Errorf("push.apps %s: apns-key %s: %w", app.AppID, app.APNsKey, err)
			}
			registry.AddApp(push.APNs, app.AppID, sender)
		}
	}
	return registry, nil
}

// Handler returns the HTTP handler, for tests and embedding.
func (s *Server) Handler() http.Handler { return s.api.Handler() }

// Close releases the message store.
func (s *Server) Close() error {
	s.endStreams()
	s.broker.Close()
	return s.store.Close()
}

// shutdownGrace is how long in-flight requests get to finish at shutdown.
const shutdownGrace = 10 * time.Second

// Serve serves on ln until ctx is cancelled, then shuts down gracefully: open
// streams and WebSockets are ended (clients reconnect and catch up), requests in
// progress finish, and the store is closed. It returns nil after a clean
// shutdown.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	defer s.Close()

	srv := &http.Server{
		Handler: s.Handler(),
		// No ReadTimeout or WriteTimeout: they would cut off long-lived streams.
		// Slow clients are handled where the data is read and written instead.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}

	purgeCtx, stopPurge := context.WithCancel(context.Background())
	defer stopPurge()
	go s.purgeLoop(purgeCtx)

	// Registered after the deferred Close, so it runs before it: notifications
	// being sent finish being recorded before the store goes away.
	if s.dispatcher != nil {
		pushCtx, stopPush := context.WithCancel(context.Background())
		pushDone := make(chan struct{})
		go func() { s.dispatcher.Run(pushCtx); close(pushDone) }()
		defer func() { stopPush(); <-pushDone }()
	}

	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(ln) }()

	select {
	case err := <-failed:
		return err // the listener broke: not a shutdown
	case <-ctx.Done():
	}

	s.log.Info("shutting down")
	// Streams and WebSockets would keep Shutdown waiting forever, so they are told
	// to end first (their clients reconnect and catch up). Requests in progress,
	// such as a publish being stored, are not interrupted: Shutdown waits for them.
	s.endStreams()
	grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(grace); err != nil {
		s.log.Warn("some requests did not finish in time; closing them", "error", err)
		srv.Close()
	}
	if err := <-failed; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// purgeLoop removes expired messages until ctx ends.
func (s *Server) purgeLoop(ctx context.Context) {
	purge := func() {
		now := s.hub.Now()
		n, err := s.store.DeleteExpired(ctx, now)
		switch {
		case ctx.Err() != nil:
		case err != nil:
			s.log.Error("could not remove expired messages", "error", err)
		case n > 0:
			s.log.Debug("removed expired messages", "count", n)
		}
		// Notifications for messages that are gone are no use to anyone.
		if jobs, err := s.store.DeleteExpiredJobs(ctx, now); err != nil && ctx.Err() == nil {
			s.log.Error("could not remove expired push notifications", "error", err)
		} else if jobs > 0 {
			s.log.Debug("removed expired push notifications", "count", jobs)
		}
	}
	purge()
	t := time.NewTicker(s.purgeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			purge()
		}
	}
}

// Run starts a server from cfg and serves until ctx is cancelled.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	s, err := New(ctx, cfg, log)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		s.Close()
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}

	path, _ := cfg.DatabasePath()
	log.Info("flare is listening", "address", ln.Addr().String(), "database", path,
		"cache", cfg.CacheDuration.String(), "default_access", cfg.Auth.DefaultAccess)
	if cfg.Open() && !loopback(cfg.Listen) {
		log.Warn("this server is open: anyone who can reach it can publish and subscribe. " +
			"To restrict it, set auth.default-access to deny-all, create users with `flare user add`, " +
			"and grant them topics with `flare access grant`; or listen on 127.0.0.1 only")
	}
	return s.Serve(ctx, ln)
}

// loopback reports whether a listen address accepts connections only from this
// machine.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
