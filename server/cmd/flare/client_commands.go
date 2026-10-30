package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vignesh-ramsh/flare/server/internal/client"
	"github.com/vignesh-ramsh/flare/server/internal/message"
)

const defaultServer = "http://localhost:8080"

// addServerFlag adds --server, which falls back to FLARE_SERVER.
func addServerFlag(cmd *cobra.Command, dst *string) {
	cmd.Flags().StringVarP(dst, "server", "s", "", "address of the Flare server (or set FLARE_SERVER; default "+defaultServer+")")
}

func newClient(c cli, server string) (*client.Client, error) {
	if server == "" {
		server = c.getenv("FLARE_SERVER")
	}
	if server == "" {
		server = defaultServer
	}
	return client.New(server)
}

// maxStdin is the most the publish command reads from standard input. The server
// has its own, much smaller, limit and says so; this just keeps a mistaken
// `cat hugefile | flare publish` from filling memory first.
const maxStdin = 1 << 20

func newPublishCmd(c cli) *cobra.Command {
	var (
		server, title, priority, tags, click string
		quiet                                bool
	)
	cmd := &cobra.Command{
		Use:   "publish [flags] <topic> [message...]",
		Short: "Send a message to a topic",
		Long: `Send a message to a topic. The message is the rest of the command line, or
standard input if there is none.

  flare publish backups "Backup finished"
  echo "disk is full" | flare publish --priority high --tags warning alerts

Prints the stored message as JSON.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(c, server)
			if err != nil {
				return err
			}
			body := strings.Join(args[1:], " ")
			if len(args) == 1 && !isTerminal(c.stdin) {
				data, err := io.ReadAll(io.LimitReader(c.stdin, maxStdin+1))
				if err != nil {
					return fmt.Errorf("read the message from standard input: %w", err)
				}
				if len(data) > maxStdin {
					return fmt.Errorf("the message on standard input is larger than %d bytes", maxStdin)
				}
				body = strings.TrimSuffix(string(data), "\n")
			}

			ctx := cmd.Context()
			m, err := cl.Publish(ctx, args[0], body, client.PublishOptions{
				Title: title, Priority: priority, Tags: message.ParseTags(tags), Click: click,
			})
			if err != nil {
				return err
			}
			if !quiet {
				return printJSON(c.stdout, m)
			}
			return nil
		},
	}
	f := cmd.Flags()
	addServerFlag(cmd, &server)
	f.StringVarP(&title, "title", "t", "", "title of the message")
	f.StringVarP(&priority, "priority", "p", "", "priority: 1-5, or min, low, default, high, urgent")
	f.StringVar(&tags, "tags", "", "comma-separated tags")
	f.StringVar(&click, "click", "", "URL to open when the notification is tapped")
	f.BoolVarP(&quiet, "quiet", "q", false, "print nothing on success")
	return cmd
}

func newSubscribeCmd(c cli) *cobra.Command {
	var (
		server, since string
		poll          bool
	)
	cmd := &cobra.Command{
		Use:   "subscribe [flags] <topic>...",
		Short: "Print messages as they arrive",
		Long: `Print the messages on one or more topics, one JSON object per line, as they
arrive. If the connection drops it reconnects and catches up on what it missed,
so nothing is lost.

  flare subscribe backups
  flare subscribe --since 1h alerts backups
  flare subscribe --poll --since all backups     print what is cached, then stop

--since takes all, a message ID, a duration such as 10m, or a unix timestamp.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(c, server)
			if err != nil {
				return err
			}
			cl.OnRetry = func(err error, wait time.Duration) {
				fmt.Fprintf(c.stderr, "connection lost (%v); retrying in %s\n", err, wait.Round(100*time.Millisecond))
			}
			topics := strings.Split(strings.Join(args, ","), ",")
			return cl.Subscribe(cmd.Context(), topics, client.SubscribeOptions{Since: since, Poll: poll},
				func(m message.Message) error { return printJSON(c.stdout, m) })
		},
	}
	f := cmd.Flags()
	addServerFlag(cmd, &server)
	f.StringVar(&since, "since", "", "start from here: all, a message ID, a duration such as 10m, or a unix timestamp (default: new messages only)")
	f.BoolVar(&poll, "poll", false, "print the cached messages and exit instead of waiting for more")
	return cmd
}

func printJSON(w io.Writer, m message.Message) error {
	return json.NewEncoder(w).Encode(m)
}

// isTerminal reports whether r is an interactive terminal, where waiting for
// input would just look like a hang.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
