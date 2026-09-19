// Command cloudctl demonstrates the strata configuration lifecycle: a single Go
// struct supplies the defaults, the file and environment tiers, and the entire
// CLI surface.
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

// DatabaseSettings holds the configuration of the nested database subsystem.
//
// Its exported fields carry flag tags, and the parent's `flag:"db"` tag exposes
// them as --db-* flags.
type DatabaseSettings struct {
	Host     string `flag:"host"      strata:"host"      toml:"host"      usage:"Database host"`
	Port     int    `flag:"port"      strata:"port"      toml:"port"      usage:"Database port"`
	MaxConns int    `flag:"max-conns" strata:"max_conns" toml:"max_conns" usage:"Connection pool size"`
}

// AppSettings is the full configuration surface.
//
// Fields with a flag tag become CLI flags. The two fields without one stay
// configuration-only and do not appear in help output.
type AppSettings struct {
	Environment string           `flag:"env,e"         strata:"env"      toml:"env"       usage:"Target environment"`
	Port        int              `flag:"port,p"        strata:"port"     toml:"port"      usage:"Server port"`
	Timeout     strata.Duration  `flag:"timeout"       strata:"timeout"  toml:"timeout"   usage:"Request timeout (30s, 7d, 1w2d)"`
	APIKey      string           `env:"API_KEY,secret" flag:"api-key"    strata:"api_key" toml:"api_key"                          usage:"API authorization key"`
	Database    DatabaseSettings `flag:"db"            strata:"database"`
	Tags        []string         `flag:"tags"          strata:"tags"     toml:"tags"      usage:"Deployment tags"`
	Verbose     bool             `flag:"verbose,v"     strata:"verbose"  toml:"verbose"   usage:"Enable verbose logging"`

	InternalState string `strata:"internal_state" toml:"internal_state"`
	CacheDir      string `strata:"cache_dir"      toml:"cache_dir"`
}

// settingsView is a loading view of AppSettings. It shares every field and tag
// but deliberately does not carry Validate: [strata.Load] must not validate a
// configuration the CLI has not contributed to yet.
//
// Validation runs once, on the merged result.
type settingsView AppSettings

// SetDefaults sets the nested database defaults.
func (d *DatabaseSettings) SetDefaults() {
	d.Host = "127.0.0.1"
	d.Port = 5432
	d.MaxConns = 20
}

// SetDefaults sets the top-level defaults.
//
// It runs before [stratacobra.RegisterFlags] so that --help reports the real
// default values, and again during loading.
func (s *AppSettings) SetDefaults() {
	s.Environment = "development"
	s.Port = 8080
	s.Timeout = strata.Duration(defaultDelay)
	s.APIKey = "dev-test-token"
	s.Tags = []string{"api", "v1"}
	s.Verbose = false
	s.CacheDir = os.TempDir()
}

// Validate enforces invariants on the fully merged configuration.
//
// It runs after the CLI has contributed its values, where a CLI flag can both
// satisfy and violate an invariant the lower layers had met.
//
// It returns [errPortRange] or [errDBPortRange], wrapped with the offending
// value, when a port is out of range, and [errDevKeyInProd] when the production
// environment keeps the development API key.
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

// SetDefaults keeps the view's defaults identical to the struct's by delegating
// to [AppSettings.SetDefaults].
func (v *settingsView) SetDefaults() {
	(*AppSettings)(v).SetDefaults()
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "cloudctl: %v\n", err)
		os.Exit(1)
	}
}

// run builds the cloudctl command tree, registers the flags derived from
// settings, and executes the root command. Command output is written to stdout.
func run(stdout io.Writer) error {
	var (
		configPath string
		settings   AppSettings
		meta       *strata.Metadata
	)

	rootCommand := newRootCommand(&configPath, &settings, &meta)
	rootCommand.SetOut(stdout)

	// Apply defaults before registration so --help reports the real values.
	settings.SetDefaults()

	// Every generated flag comes from AppSettings. Persistent registration makes
	// the flags available to subcommands as well.
	if err := stratacobra.RegisterFlags(rootCommand, &settings, stratacobra.WithPersistent()); err != nil {
		return fmt.Errorf("register flags: %w", err)
	}

	// Registration is additive. Flags that are not settings are declared through
	// the framework's own API and are left untouched by the bridge.
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

// load performs the configuration lifecycle for every command that needs it.
//
// Flags the user supplied are overlaid onto the loaded configuration before
// validation, and the flags the user omitted are filled from the merged result.
func load(command *cobra.Command, configPath string, settings *AppSettings, meta **strata.Metadata) error {
	options := []strata.Option{
		strata.WithAppName("cloudctl"),
		strata.WithEnvPrefix("CLOUD_"),
	}

	if configPath != "" {
		options = append(options, strata.WithExplicitPath(configPath))
	}

	// 1. Load the lower layers through the view, which defers validation.
	view, loaded, err := strata.Load[settingsView](options...)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	*settings = AppSettings(view)
	*meta = loaded

	// 2. Overlay the CLI values onto the loaded configuration. This MUST run
	//    before Apply: Apply writes through the framework's setters, which mark
	//    flags as supplied and would hide which flags the user passed.
	if err := stratacobra.SyncFlagsToStruct(command, settings, stratacobra.WithMetadata(*meta)); err != nil {
		return fmt.Errorf("synchronize flags: %w", err)
	}

	// 3. Validate the merged result, where a CLI value can satisfy or violate an
	//    invariant the lower layers met.
	if err := settings.Validate(); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}

	// 4. Push configuration into the flags the user omitted so that cmd.Flags()
	//    and the settings struct agree.
	if err := stratacobra.Apply(command, *settings); err != nil {
		return fmt.Errorf("apply configuration: %w", err)
	}

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

// newSetCommand builds the "config set" command, which rewrites a single key
// through [strata.Set].
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

// printSummary writes the resolved settings to out.
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

// printOrigins writes the origin of a fixed set of keys, skipping any the loader
// did not record. An origin with no file path is reported as <built-in>.
func printOrigins(out io.Writer, meta *strata.Metadata) error {
	var report strings.Builder
	report.WriteString("\norigins:\n")

	for _, key := range []string{"port", "env", "timeout", "api_key", "database.port"} {
		origin, ok := meta.Where(key)
		if !ok {
			continue
		}

		location := origin.Path
		if location == "" {
			location = "<built-in>"
		}

		fmt.Fprintf(&report, "  %-14s %-8s %-24s %s\n", key, origin.Source, location, origin.RawValue)
	}

	if _, err := io.WriteString(out, report.String()); err != nil {
		return fmt.Errorf("write origins: %w", err)
	}

	return nil
}
