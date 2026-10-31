package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vignesh-ramsh/flare/server/internal/config"
)

// configFlags are the options that say which configuration to use. The server and
// the commands that manage users both take them, so that the commands find the
// same database the server uses.
type configFlags struct {
	path, listen, baseURL, database string
}

// addTo registers the flags on cmd. The server also takes --listen and --base-url.
func (f *configFlags) addTo(cmd *cobra.Command, forServer bool) {
	fl := cmd.Flags()
	fl.StringVarP(&f.path, "config", "c", "", "path of the YAML configuration file (or set FLARE_CONFIG)")
	fl.StringVar(&f.database, "database", "", "message store, such as sqlite:///var/lib/flare/flare.db")
	if forServer {
		fl.StringVar(&f.listen, "listen", "", "address to listen on, such as :8080 or 127.0.0.1:8080")
		fl.StringVar(&f.baseURL, "base-url", "", "public address of the server, such as https://flare.example.com")
	}
}

// load builds the configuration: defaults, then the file, then FLARE_*
// environment variables, then the flags that were given.
func (f *configFlags) load(c cli, cmd *cobra.Command) (config.Config, error) {
	path := f.path
	if path == "" {
		path = c.getenv("FLARE_CONFIG")
	}
	cfg, err := config.Load(path)
	if err != nil {
		return cfg, err
	}
	if err := cfg.ApplyEnv(c.getenv); err != nil {
		return cfg, err
	}
	flags := cmd.Flags()
	if flags.Changed("listen") {
		cfg.Listen = f.listen
	}
	if flags.Changed("base-url") {
		cfg.BaseURL = f.baseURL
	}
	if flags.Changed("database") {
		cfg.Database = f.database
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("invalid configuration:\n  %s", strings.ReplaceAll(err.Error(), "\n", "\n  "))
	}
	return cfg, nil
}
