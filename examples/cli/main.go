package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/strata"
	stratacobra "github.com/zigai/strata/bridge/cobra"
)

const (
	portMinimum  = 1
	portMaximum  = 65535
	setArgCount  = 3
	defaultDelay = 30 * time.Second
)

var (
	errPortRange    = errors.New("port must be between 1 and 65535")
	errDBPortRange  = errors.New("database port must be between 1 and 65535")
	errDevKeyInProd = errors.New("cannot use the development API key in production")
)

type DatabaseSettings struct {
	Host     string `flag:"host"      usage:"Database host"`
	Port     int    `flag:"port"      usage:"Database port"`
	MaxConns int    `flag:"max-conns" usage:"Connection pool size"`
}

type AppSettings struct {
	Environment string           `flag:"env,e"         strata:"env"                            usage:"Target environment"`
	Port        int              `flag:"port,p"        usage:"Server port"`
	Timeout     strata.Duration  `flag:"timeout"       usage:"Request timeout (30s, 7d, 1w2d)"`
	APIKey      string           `env:"API_KEY,secret" flag:"api-key"                          usage:"API authorization key"`
	Database    DatabaseSettings `flag:"db"`
	Tags        []string         `flag:"tags"          usage:"Deployment tags"`
	Verbose     bool             `flag:"verbose,v"     usage:"Enable verbose logging"`

	InternalState string
	CacheDir      string
}

func (d *DatabaseSettings) SetDefaults() {
	d.Host = "127.0.0.1"
	d.Port = 5432
	d.MaxConns = 20
}

func (s *AppSettings) SetDefaults() {
	s.Environment = "development"
	s.Port = 8080
	s.Timeout = strata.Duration(defaultDelay)
	s.APIKey = "dev-test-token"
	s.Tags = []string{"api", "v1"}
	s.Verbose = false
	s.CacheDir = os.TempDir()
}

func (s *AppSettings) Validate() error {
	if s.Port < portMinimum || s.Port > portMaximum {
		return fmt.Errorf("%w: got %d", errPortRange, s.Port)
	}

	if s.Database.Port < portMinimum || s.Database.Port > portMaximum {
		return fmt.Errorf("%w: got %d", errDBPortRange, s.Database.Port)
	}

	if s.Environment == "production" && s.APIKey == "dev-test-token" {
		return errDevKeyInProd
	}

	return nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "cloudctl: %v\n", err)
		os.Exit(1)
	}
}

func run(stdout io.Writer) error {
	var (
		configPath string
		settings   AppSettings
		meta       *strata.Metadata
	)

	rootCommand := newRootCommand(&configPath, &settings, &meta)
	rootCommand.SetOut(stdout)

	settings.SetDefaults()

	if err := stratacobra.RegisterFlags(rootCommand, &settings, stratacobra.WithPersistent()); err != nil {
		return fmt.Errorf("register flags: %w", err)
	}

	rootCommand.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Explicit config file path")

	if err := rootCommand.Execute(); err != nil {
		return fmt.Errorf("execute: %w", err)
	}

	return nil
}

func newRootCommand(configPath *string, settings *AppSettings, meta **strata.Metadata) *cobra.Command {
	rootCommand := &cobra.Command{
		Use:   "cloudctl",
		Short: "A sample CLI driven entirely by a strata configuration struct",
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			return load(command, *configPath, settings, meta)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return printSummary(command.OutOrStdout(), settings)
		},
	}

	rootCommand.AddCommand(newConfigCommand(settings, meta))

	return rootCommand
}

func load(command *cobra.Command, configPath string, settings *AppSettings, meta **strata.Metadata) error {
	options := []strata.Option{
		strata.WithAppName("cloudctl"),
		strata.WithEnvPrefix("CLOUD_"),
		stratacobra.WithFlags(command),
	}

	if configPath != "" {
		options = append(options, strata.WithPath(configPath))
	}

	loadedSettings, loadedMeta, err := strata.LoadWithMetadata[AppSettings](options...)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	*settings = loadedSettings
	*meta = loadedMeta

	return nil
}

func newConfigCommand(settings *AppSettings, meta **strata.Metadata) *cobra.Command {
	configCommand := &cobra.Command{
		Use:   "config",
		Short: "Inspect or mutate the configuration",
	}

	configCommand.AddCommand(newShowCommand(settings, meta))
	configCommand.AddCommand(newSetCommand())

	return configCommand
}

func newShowCommand(settings *AppSettings, meta **strata.Metadata) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show resolved settings and where each value came from",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := printSummary(command.OutOrStdout(), settings); err != nil {
				return err
			}

			return printOrigins(command.OutOrStdout(), *meta)
		},
	}
}

func newSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set <file> <key> <value>",
		Short: "Update one key in place, preserving comments and formatting",
		Args:  cobra.ExactArgs(setArgCount),
		RunE: func(command *cobra.Command, args []string) error {
			if err := strata.Set(args[0], args[1], args[2]); err != nil {
				return fmt.Errorf("set %s: %w", args[1], err)
			}

			if _, err := fmt.Fprintf(command.OutOrStdout(), "updated %s in %s\n", args[1], args[0]); err != nil {
				return fmt.Errorf("write result: %w", err)
			}

			return nil
		},
	}
}

func printSummary(out io.Writer, settings *AppSettings) error {
	summary := fmt.Sprintf(
		"cloudctl on :%d [env: %s]\n  database : %s:%d (max conns %d)\n  timeout  : %s\n  tags     : %v\n  cache    : %s\n  verbose  : %t\n",
		settings.Port, settings.Environment,
		settings.Database.Host, settings.Database.Port, settings.Database.MaxConns,
		settings.Timeout.String(), settings.Tags, settings.CacheDir, settings.Verbose,
	)

	if _, err := io.WriteString(out, summary); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}

	return nil
}

func printOrigins(out io.Writer, meta *strata.Metadata) error {
	var report strings.Builder
	report.WriteString("\norigins:\n")

	for _, origin := range meta.Origins() {
		location := origin.Path
		if location == "" {
			location = "<built-in>"
		}

		fmt.Fprintf(&report, "  %-18s %-8s %-24s %s\n", origin.Key, origin.Source, location, origin.RawValue)
	}

	for _, unknown := range meta.UnknownKeys() {
		fmt.Fprintf(&report, "  warning: %s sets unknown key %q", unknown.Path, unknown.Key)

		if unknown.Suggestion != "" {
			fmt.Fprintf(&report, " (did you mean %q?)", unknown.Suggestion)
		}

		report.WriteString("\n")
	}

	if _, err := io.WriteString(out, report.String()); err != nil {
		return fmt.Errorf("write origins: %w", err)
	}

	return nil
}
