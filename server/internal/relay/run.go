package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/push"
)

// shutdownGrace is how long requests in progress get to finish at shutdown.
const shutdownGrace = 10 * time.Second

// Run starts a relay from cfg and serves until ctx is cancelled.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	r, closeStore, err := build(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeStore()
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}
	log.Info("flare push relay is listening", "address", ln.Addr().String(), "services", r.Senders.Names())
	return Serve(ctx, r, ln, log)
}

// build opens the relay's database and loads the credentials of the apps it serves.
func build(ctx context.Context, cfg config.Config, log *slog.Logger) (*Relay, func(), error) {
	if len(cfg.Push.Apps) == 0 {
		return nil, nil, errors.New("a relay needs the credentials of the apps it wakes: list them under push.apps")
	}
	senders := push.NewRegistry()
	client := push.NewHTTP2Client()
	for _, app := range cfg.Push.Apps {
		if err := push.AddApp(senders, app.Credentials(), client); err != nil {
			return nil, nil, err
		}
	}
	proxies, err := auth.ParseProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, nil, err
	}
	path, err := cfg.RelayDatabasePath()
	if err != nil {
		return nil, nil, err
	}
	st, err := OpenStore(ctx, path)
	if err != nil {
		return nil, nil, fmt.Errorf("open the relay's database: %w", err)
	}
	r := New(Config{
		Senders: senders, Store: st, Proxies: proxies, Log: log,
		WakesPerHandle: cfg.Relay.WakesPerHandlePerMinute, WakesPerIP: cfg.Relay.WakesPerIPPerMinute,
		RegistrationsPerIP: cfg.Relay.RegistrationsPerIPPerMinute,
	})
	return r, func() { st.Close() }, nil
}

// Serve serves r on ln until ctx is cancelled, then lets requests in progress
// finish. It returns nil after a clean shutdown.
func Serve(ctx context.Context, r *Relay, ln net.Listener, log *slog.Logger) error {
	srv := &http.Server{
		Handler:           r.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second, // requests are small, unlike a server's streams
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(ln) }()
	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(grace); err != nil {
		log.Warn("some requests did not finish in time; closing them", "error", err)
		srv.Close()
	}
	if err := <-failed; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
