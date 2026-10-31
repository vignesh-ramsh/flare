package main

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
	"github.com/vignesh-ramsh/flare/server/internal/store"
)

// The user, token and access commands work directly on the database, on the
// machine the server runs on. They don't go through the network, so they need no
// credentials of their own: being able to reach the database is the authority.
// They can run while the server is up; it reads the changes at once.

// withAccounts opens the database the configuration names and gives fn an auth
// service over it.
func withAccounts(c cli, cmd *cobra.Command, flags *configFlags, fn func(*auth.Service) error) error {
	cfg, err := flags.load(c, cmd)
	if err != nil {
		return err
	}
	path, err := cfg.DatabasePath()
	if err != nil {
		return err
	}
	st, err := store.OpenSQLite(cmd.Context(), path)
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	defer st.Close()
	access, err := auth.ParseAccess(cfg.Auth.DefaultAccess)
	if err != nil {
		return err
	}
	return fn(auth.NewService(st, auth.ServiceConfig{Default: access, HashParams: c.hashParams}))
}

// adminGroup creates a command with subcommands that share the configuration flags.
func adminGroup(use, short, long string, flags *configFlags) *cobra.Command {
	cmd := &cobra.Command{Use: use, Short: short, Long: long}
	fl := cmd.PersistentFlags()
	fl.StringVarP(&flags.path, "config", "c", "", "path of the YAML configuration file (or set FLARE_CONFIG)")
	fl.StringVar(&flags.database, "database", "", "the database to work on, such as sqlite:///var/lib/flare/flare.db")
	return cmd
}

func newTable(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) }

func when(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// --- flare user ---------------------------------------------------------------

func newUserCmd(c cli) *cobra.Command {
	var flags configFlags
	cmd := adminGroup("user", "Manage users",
		`Create, list, disable and delete users, set their passwords and roles. These work on the
database directly, so run them on the server's machine (in Docker:
docker compose exec flare flare user add alice).

The server's access policy decides what users may do; see "flare access".`, &flags)

	var admin bool
	add := &cobra.Command{
		Use:   "add [--admin] <name>",
		Short: "Create a user",
		Long: `Create a user. The password is asked for at the prompt, or read from standard
input if that is piped; it is never taken from the command line, where other users
could see it. An administrator may read and write every topic.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				password, err := readPassword(c, "Password for "+args[0]+": ", true)
				if err != nil {
					return err
				}
				role := auth.RoleUser
				if admin {
					role = auth.RoleAdmin
				}
				u, err := svc.CreateUser(cmd.Context(), args[0], password, role)
				if err != nil {
					return err
				}
				fmt.Fprintf(c.stderr, "Created %s %s.\n", u.Role, u.Name)
				return nil
			})
		},
	}
	add.Flags().BoolVar(&admin, "admin", false, "make the user an administrator")

	list := &cobra.Command{
		Use:   "list",
		Short: "List users",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				users, err := svc.Users(cmd.Context())
				if err != nil {
					return err
				}
				tw := newTable(c.stdout)
				fmt.Fprintln(tw, "NAME\tROLE\tSTATE\tCREATED")
				for _, u := range users {
					state := "active"
					if u.Disabled {
						state = "disabled"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", u.Name, u.Role, state, when(&u.CreatedAt))
				}
				return tw.Flush()
			})
		},
	}

	passwd := &cobra.Command{
		Use:   "passwd <name>",
		Short: "Change a user's password",
		Long: `Change a user's password. Anything logged in with the old password is cut off
within half a minute; access tokens keep working, and are revoked separately with
"flare token delete".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				if _, err := svc.FindUser(cmd.Context(), args[0]); err != nil {
					return err
				}
				password, err := readPassword(c, "New password for "+args[0]+": ", true)
				if err != nil {
					return err
				}
				if err := svc.SetPassword(cmd.Context(), args[0], password); err != nil {
					return err
				}
				fmt.Fprintf(c.stderr, "Changed the password of %s.\n", args[0])
				return nil
			})
		},
	}

	del := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a user, with their tokens and access rules",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				if err := svc.DeleteUser(cmd.Context(), args[0]); err != nil {
					return err
				}
				fmt.Fprintf(c.stderr, "Deleted %s, with their tokens and access rules.\n", args[0])
				return nil
			})
		},
	}
	role := &cobra.Command{
		Use:   "role <name> <user|admin>",
		Short: "Make a user an administrator, or an ordinary user",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				if err := svc.SetRole(cmd.Context(), args[0], auth.Role(args[1])); err != nil {
					return err
				}
				fmt.Fprintf(c.stderr, "%s is now %s.\n", args[0], args[1])
				return nil
			})
		},
	}

	disable := &cobra.Command{
		Use:   "disable <name>",
		Short: "Stop a user logging in, without deleting anything",
		Long: `Disable a user. They cannot log in, their tokens stop working, and what they have
open is cut off within half a minute. Their account, tokens and access rules are
kept: "flare user enable" restores everything.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				if err := svc.SetDisabled(cmd.Context(), args[0], true); err != nil {
					return err
				}
				fmt.Fprintf(c.stderr, "Disabled %s.\n", args[0])
				return nil
			})
		},
	}

	enable := &cobra.Command{
		Use:   "enable <name>",
		Short: "Let a disabled user log in again",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				if err := svc.SetDisabled(cmd.Context(), args[0], false); err != nil {
					return err
				}
				fmt.Fprintf(c.stderr, "Enabled %s.\n", args[0])
				return nil
			})
		},
	}
	cmd.AddCommand(add, list, passwd, role, disable, enable, del)
	return cmd
}

// --- flare token --------------------------------------------------------------

func newTokenCmd(c cli) *cobra.Command {
	var flags configFlags
	cmd := adminGroup("token", "Manage access tokens",
		`Create, list and revoke access tokens. A token lets a program or device act as a
user without knowing their password, and can be revoked on its own. Users can also
get their own with "flare login".`, &flags)

	var label, expires string
	add := &cobra.Command{
		Use:   "add [flags] <user>",
		Short: "Create an access token for a user",
		Long: `Create an access token for a user and print it. It is shown only this once: only
a hash of it is kept.

  FLARE_TOKEN=$(flare token add --label backup-server alice)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lifetime, err := parseLifetime(expires)
			if err != nil {
				return err
			}
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				u, err := svc.FindUser(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				secret, tok, err := svc.IssueToken(cmd.Context(), u.ID, label, lifetime)
				if err != nil {
					return err
				}
				fmt.Fprintf(c.stderr, "Created token %s for %s. It is shown only once; keep it safe.\n", tok.ID, u.Name)
				fmt.Fprintln(c.stdout, secret)
				return nil
			})
		},
	}
	add.Flags().StringVar(&label, "label", "", "a name for the token, such as where it is used")
	add.Flags().StringVar(&expires, "expires", "", "how long it lasts, such as 30d or 12h (default: until revoked)")

	list := &cobra.Command{
		Use:   "list <user>",
		Short: "List a user's tokens",
		Long:  "List a user's tokens by ID. The tokens themselves are not kept, so they can't be shown.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				u, err := svc.FindUser(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				tokens, err := svc.Tokens(cmd.Context(), u.ID)
				if err != nil {
					return err
				}
				tw := newTable(c.stdout)
				fmt.Fprintln(tw, "ID\tLABEL\tCREATED\tEXPIRES\tLAST USED")
				for _, t := range tokens {
					created := t.CreatedAt
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Label, when(&created), when(t.ExpiresAt), when(t.LastUsedAt))
				}
				return tw.Flush()
			})
		},
	}

	del := &cobra.Command{
		Use:   "delete <token-id>",
		Short: "Revoke a token",
		Long:  "Revoke a token by its ID (see \"flare token list\"). Whatever uses it is cut off within half a minute.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				err := svc.DeleteToken(cmd.Context(), args[0], "")
				if errors.Is(err, auth.ErrNotFound) {
					return fmt.Errorf("there is no token with the ID %q", args[0])
				}
				if err != nil {
					return err
				}
				fmt.Fprintf(c.stderr, "Revoked token %s.\n", args[0])
				return nil
			})
		},
	}
	cmd.AddCommand(add, list, del)
	return cmd
}

// --- flare access -------------------------------------------------------------

func newAccessCmd(c cli) *cobra.Command {
	var flags configFlags
	cmd := adminGroup("access", "Manage which topics users may read and write",
		`Access rules say what a user, or everyone, may do with the topics that match a
pattern, where * stands for any characters:

  flare access grant alice 'alerts-*' read-write
  flare access grant everyone status read-only
  flare access grant bob secret deny-all

The levels are read-write, read-only, write-only and deny-all. Of the rules that
match a topic, the most specific decides: an exact name beats a pattern, a longer
pattern beats a shorter one, and at equal specificity a user's own rule beats the
one for everyone and a deny beats an allow. Where no rule matches, the server's
auth.default-access applies. Administrators may do everything.`, &flags)

	grant := &cobra.Command{
		Use:   "grant <user|everyone> <pattern> <read-write|read-only|write-only|deny-all>",
		Short: "Give a user, or everyone, access to topics",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			access, err := auth.ParseAccess(args[2])
			if err != nil {
				return fmt.Errorf("the access level %v", err)
			}
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				if err := svc.Grant(cmd.Context(), args[0], args[1], access); err != nil {
					return err
				}
				fmt.Fprintf(c.stderr, "%s now has %s on %q.\n", args[0], access, args[1])
				return nil
			})
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke <user|everyone> <pattern>",
		Short: "Remove a rule",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				if err := svc.Revoke(cmd.Context(), args[0], args[1]); err != nil {
					return err
				}
				fmt.Fprintf(c.stderr, "Removed the rule for %q from %s.\n", args[1], args[0])
				return nil
			})
		},
	}

	list := &cobra.Command{
		Use:   "list [user|everyone]",
		Short: "List access rules",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAccounts(c, cmd, &flags, func(svc *auth.Service) error {
				rules, err := svc.AllRules(cmd.Context())
				if err != nil {
					return err
				}
				tw := newTable(c.stdout)
				fmt.Fprintln(tw, "WHO\tPATTERN\tACCESS")
				for _, r := range rules {
					if len(args) == 1 && r.Who != args[0] {
						continue
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Who, r.Pattern, r.Access)
				}
				return tw.Flush()
			})
		},
	}
	cmd.AddCommand(grant, revoke, list)
	return cmd
}
