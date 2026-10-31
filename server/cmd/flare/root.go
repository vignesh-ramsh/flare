package main

import (
	"io"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

// cli is everything the commands touch outside themselves, so that tests can
// substitute it.
type cli struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
	// hashParams are the password hashing costs for commands that set passwords.
	// Zero means the production settings; tests use cheap ones.
	hashParams auth.Params
}

func newRootCmd(c cli) *cobra.Command {
	root := &cobra.Command{
		Use:   "flare",
		Short: "Flare: push notifications to your phone and desktop with a simple HTTP request",
		Long: `Flare is a publish/subscribe notification server and its command-line client.

  flare serve                       run the server
  flare relay                       run a push relay for an app you publish
  flare publish backups "done"      send a message to a topic
  flare subscribe backups           print messages as they arrive

Anything that can make an HTTP request can publish:

  curl -d "Backup finished" https://flare.example.com/backups`,
		Version:       versionString(),
		SilenceUsage:  true, // errors are reported by main, not with a usage dump
		SilenceErrors: true,
	}
	root.SetOut(c.stdout)
	root.SetErr(c.stderr)
	root.SetIn(c.stdin)
	root.SetVersionTemplate("flare {{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(newServeCmd(c), newPublishCmd(c), newSubscribeCmd(c), newLoginCmd(c),
		newUserCmd(c), newTokenCmd(c), newAccessCmd(c), newRelayCmd(c))
	return root
}

// versionString is the release version, or for a development build the commit
// it was built from.
func versionString() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		var rev, dirty string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "-dirty"
				}
			}
		}
		if len(rev) >= 7 {
			return "dev-" + rev[:7] + dirty
		}
	}
	return version
}
