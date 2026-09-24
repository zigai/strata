package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/zigai/strata"
	stratacobra "github.com/zigai/strata/bridge/cobra"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "myapp:", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var cfg Config

	root := &cobra.Command{
		Use:           "myapp",
		Short:         "A small service configured with strata",
		SilenceErrors: true,
	}

	// Adds -c/--config and loads defaults, config files, MYAPP_* variables,
	// and changed flags before commands under root run.
	b := stratacobra.Bind(
		root, &cfg,
		strata.WithAppName("myapp"),
		strata.WithFormats("toml"),
		strata.WithEnvPrefix("MYAPP_"),
	)

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Start the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A real service would start listening here.
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "serving %s on :%d (db %s:%d, timeout %s)\n", cfg.Env, cfg.Port, cfg.DB.Host, cfg.DB.Port, cfg.Timeout)

			return err
		},
	}

	// Each flag names one config key. Its type and --help default come from Config.
	f := serve.Flags()
	b.FlagP(f, "env", "e", "deployment environment")
	b.FlagP(f, "port", "p", "port to listen on")
	b.Flag(f, "timeout", "request timeout")
	b.FlagP(f, "verbose", "v", "log every request")
	b.Flag(f, "db.host", "database host")
	b.Flag(f, "db.port", "database port")
	b.Flag(f, "db.max_conns", "maximum open connections")

	// config show | set <key> <value> | init | path
	root.AddCommand(serve, b.ConfigCommand())

	return root
}
