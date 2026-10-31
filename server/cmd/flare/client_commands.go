package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vignesh-ramsh/flare/server/internal/client"
	"github.com/vignesh-ramsh/flare/server/internal/message"
)

const defaultServer = "http://localhost:8080"

// connection is the options that say which server to talk to, and as whom.
type connection struct{ server, token string }

// addTo adds --server, which falls back to FLARE_SERVER, and --token, which falls
// back to FLARE_TOKEN. Prefer the environment variable for the token: a command
// line can be seen by other users on the machine, and is kept in shell history.
func (k *connection) addTo(cmd *cobra.Command, withToken bool) {
	cmd.Flags().StringVarP(&k.server, "server", "s", "", "address of the Flare server (or set FLARE_SERVER; default "+defaultServer+")")
	if withToken {
		cmd.Flags().StringVar(&k.token, "token", "", "access token (better set as FLARE_TOKEN, which isn't visible in the process list)")
	}
}

func (k *connection) client(c cli) (*client.Client, error) {
	server := k.server
	if server == "" {
		server = c.getenv("FLARE_SERVER")
	}
	if server == "" {
		server = defaultServer
	}
	cl, err := client.New(server)
	if err != nil {
		return nil, err
	}
	cl.Token = k.token
	if cl.Token == "" {
		cl.Token = c.getenv("FLARE_TOKEN")
	}
	return cl, nil
}

// maxStdin is the most the publish command reads from standard input. The server
// has its own, much smaller, limit and says so; this just keeps a mistaken
// `cat hugefile | flare publish` from filling memory first.
const maxStdin = 1 << 20

func newPublishCmd(c cli) *cobra.Command {
	var (
		conn                         connection
		title, priority, tags, click string
		quiet                        bool
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
			cl, err := conn.client(c)
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
	conn.addTo(cmd, true)
	f.StringVarP(&title, "title", "t", "", "title of the message")
	f.StringVarP(&priority, "priority", "p", "", "priority: 1-5, or min, low, default, high, urgent")
	f.StringVar(&tags, "tags", "", "comma-separated tags")
	f.StringVar(&click, "click", "", "URL to open when the notification is tapped")
	f.BoolVarP(&quiet, "quiet", "q", false, "print nothing on success")
	return cmd
}

func newSubscribeCmd(c cli) *cobra.Command {
	var (
		conn  connection
		since string
		poll  bool
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
			cl, err := conn.client(c)
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
	conn.addTo(cmd, true)
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

func newLoginCmd(c cli) *cobra.Command {
	var (
		conn    connection
		label   string
		expires string
	)
	cmd := &cobra.Command{
		Use:   "login [flags] <user>",
		Short: "Log in and print an access token",
		Long: `Log in with a user name and password and print an access token, to use with
--token or the FLARE_TOKEN environment variable. The password is asked for at the
prompt, or read from standard input if that is piped; it is never taken from the
command line.

  export FLARE_TOKEN=$(flare login --server https://flare.example.com alice)

Tokens can be listed and revoked one at a time, so each device should have its
own, with --label saying which.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := conn.client(c)
			if err != nil {
				return err
			}
			lifetime, err := parseLifetime(expires)
			if err != nil {
				return err
			}
			password, err := readPassword(c, "Password for "+args[0]+": ", false)
			if err != nil {
				return err
			}
			res, err := cl.Login(cmd.Context(), args[0], password, client.LoginOptions{Label: label, ExpiresIn: lifetime})
			if err != nil {
				return err
			}
			fmt.Fprintf(c.stderr, "Logged in as %s. The token below is shown only once.\n", res.User)
			fmt.Fprintln(c.stdout, res.Token)
			return nil
		},
	}
	conn.addTo(cmd, false)
	cmd.Flags().StringVar(&label, "label", "", "a name for the token, such as the device it is for")
	cmd.Flags().StringVar(&expires, "expires", "", "how long the token lasts, such as 30d or 12h (default: until revoked)")
	return cmd
}

// parseLifetime reads a duration that may be given in days ("30d"), which
// time.ParseDuration doesn't know. An empty string means no expiry.
func parseLifetime(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 || n > 3650 {
			return 0, fmt.Errorf("invalid lifetime %q: use a number of days from 1 to 3650, like 30d", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid lifetime %q: use a duration such as 12h or 30d", s)
	}
	return d, nil
}
