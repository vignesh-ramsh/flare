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
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/api"
	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/broker"
	"github.com/vignesh-ramsh/flare/server/internal/config"
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

	br := broker.NewInProc()
	hub := realtime.NewHub(st, br, realtime.Options{TTL: cfg.CacheDuration, MaxMessageSize: cfg.MessageSizeLimit})
	shutdown, endStreams := context.WithCancel(context.Background())
	return &Server{
		cfg: cfg, log: log, store: st, broker: br, hub: hub,
		api: api.New(api.Deps{
			Hub: hub, Config: cfg, Log: log, Shutdown: shutdown,
			Auth: auth.NewService(st, auth.ServiceConfig{Default: access, Proxies: proxies}),
		}),
		shutdown: shutdown, endStreams: endStreams,
		purgeEvery: time.Minute,
	}, nil
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
		n, err := s.store.DeleteExpired(ctx, s.hub.Now())
		switch {
		case ctx.Err() != nil:
		case err != nil:
			s.log.Error("could not remove expired messages", "error", err)
		case n > 0:
			s.log.Debug("removed expired messages", "count", n)
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
