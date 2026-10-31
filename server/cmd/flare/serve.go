package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/vignesh-ramsh/flare/server/internal/server"
)

func newServeCmd(c cli) *cobra.Command {
	var flags configFlags
	var logFormat, logLevel string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Flare server",
		Long: `Run the Flare server until interrupted.

Settings come from built-in defaults, then the YAML file given with --config
(or FLARE_CONFIG), then FLARE_* environment variables, then these flags. A
setting that is misspelled or not supported yet is an error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := flags.load(c, cmd)
			if err != nil {
				return err
			}
			log, err := newLogger(c.stderr, logFormat, logLevel)
			if err != nil {
				return err
			}
			return server.Run(cmd.Context(), cfg, log)
		},
	}
	f := cmd.Flags()
	flags.addTo(cmd, true)
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
