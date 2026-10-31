package main

import (
	"github.com/spf13/cobra"

	"github.com/vignesh-ramsh/flare/server/internal/relay"
)

func newRelayCmd(c cli) *cobra.Command {
	var flags configFlags
	var logFormat, logLevel string
	cmd := &cobra.Command{
		Use:   "relay",
		Short: "Run a push relay for an app you publish",
		Long: `Run a push relay: a small server that holds the Firebase and Apple credentials of
the apps you publish, so that Flare servers run by other people can wake those
apps' devices without having the credentials themselves.

The apps give the relay their push tokens and get opaque handles to hand to the
servers they use. A server asks the relay to wake a handle, naming only a
subscription and a message by opaque IDs; the relay sends a notification that
holds just those. It never learns what was published or where, and the server
never learns the push token.

The credentials go under push.apps in the configuration file, as for a server. A
server lists the relay under push.allowed-relays to use it. The relay keeps its
handles in relay.database, which is not a server's database. Settings come from
defaults, then --config (or FLARE_CONFIG), then FLARE_* variables, then flags.`,
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
			return relay.Run(cmd.Context(), cfg, log)
		},
	}
	f := cmd.Flags()
	flags.addTo(cmd, true)
	f.StringVar(&logFormat, "log-format", "json", "log format: json or text")
	f.StringVar(&logLevel, "log-level", "info", "log level: debug, info, warn or error")
	return cmd
}
