package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vignesh-ramsh/flare/server/internal/config"
	"github.com/vignesh-ramsh/flare/server/internal/server"
)

func newServeCmd(c cli) *cobra.Command {
	var (
		configPath, listen, baseURL, database string
		logFormat, logLevel                   string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Flare server",
		Long: `Run the Flare server until interrupted.

Settings come from built-in defaults, then the YAML file given with --config
(or FLARE_CONFIG), then FLARE_* environment variables, then these flags. A
setting that is misspelled or not supported yet is an error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if configPath == "" {
				configPath = c.getenv("FLARE_CONFIG")
			}
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if err := cfg.ApplyEnv(c.getenv); err != nil {
				return err
			}
			// Flags win, but only the ones actually given.
			flags := cmd.Flags()
			if flags.Changed("listen") {
				cfg.Listen = listen
			}
			if flags.Changed("base-url") {
				cfg.BaseURL = baseURL
			}
			if flags.Changed("database") {
				cfg.Database = database
			}
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid configuration:\n  %s", strings.ReplaceAll(err.Error(), "\n", "\n  "))
			}

			log, err := newLogger(c.stderr, logFormat, logLevel)
			if err != nil {
				return err
			}
			return server.Run(cmd.Context(), cfg, log)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&configPath, "config", "c", "", "path of the YAML configuration file (or set FLARE_CONFIG)")
	f.StringVar(&listen, "listen", "", "address to listen on, such as :8080 or 127.0.0.1:8080")
	f.StringVar(&baseURL, "base-url", "", "public address of the server, such as https://flare.example.com")
	f.StringVar(&database, "database", "", "message store, such as sqlite:///var/lib/flare/flare.db")
	f.StringVar(&logFormat, "log-format", "json", "log format: json or text")
	f.StringVar(&logLevel, "log-level", "info", "log level: debug, info, warn or error")
	return cmd
}

// newLogger builds the server's logger. Logs go to stderr, as is usual for
// services, and never contain message contents or tokens.
func newLogger(w io.Writer, format, level string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid --log-level %q: use debug, info, warn or error", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	}
	return nil, errors.New("invalid --log-format " + format + ": use json or text")
}
