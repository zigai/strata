package stratacobra_test

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/strata"
	stratacobra "github.com/zigai/strata/bridge/cobra"
	"github.com/zigai/strata/internal/bridgetest"
)

type cobraApp struct {
	*stratacobra.Binding[bridgetest.Config]

	root *cobra.Command
}

func newCobraApp(_ *testing.T, cfg *bridgetest.Config, app bridgetest.App) bridgetest.Instance {
	root := &cobra.Command{Use: bridgetest.AppName, SilenceErrors: true, SilenceUsage: true}

	options := append([]strata.Option{strata.WithFormats("toml")}, app.Options...)
	b := stratacobra.Bind(root, cfg, options...)

	for _, name := range slices.Sorted(maps.Keys(app.Commands)) {
		cmd := &cobra.Command{Use: name, RunE: noop}
		for _, flag := range app.Commands[name] {
			b.FlagP(cmd.Flags(), flag.Key, flag.Short, flag.Key)
		}

		root.AddCommand(cmd)
	}

	ping := &cobra.Command{Use: "ping", RunE: noop}
	ping.Flags().String("port", "", "remote port")
	root.AddCommand(ping, b.ConfigCommand())

	return &cobraApp{Binding: b, root: root}
}

func (a *cobraApp) Run(args ...string) (string, error) {
	var out bytes.Buffer

	a.root.SetOut(&out)
	a.root.SetErr(io.Discard)
	a.root.SetArgs(args)
	err := a.root.Execute()

	return out.String(), err
}

func noop(*cobra.Command, []string) error { return nil }

func TestCobraSharedBehavior(t *testing.T) {
	bridgetest.Run(t, newCobraApp)
}

func TestCobraRequiresFormatsOnExecute(t *testing.T) {
	var cfg bridgetest.Config

	root := &cobra.Command{Use: bridgetest.AppName, SilenceErrors: true, SilenceUsage: true, RunE: noop}
	stratacobra.Bind(root, &cfg)
	root.SetArgs(nil)

	if err := root.Execute(); !errors.Is(err, strata.ErrNoFormats) {
		t.Fatalf("Execute error = %v, want ErrNoFormats", err)
	}
}

// Help shows each flag's default from SetDefaults, nested structs included, and
// a strata.Duration flag is a duration flag.
func TestCobraHelpShowsDefaultsAndTypes(t *testing.T) {
	var cfg bridgetest.Config

	root := &cobra.Command{Use: bridgetest.AppName}
	b := stratacobra.Bind(root, &cfg, strata.WithFormats("toml"))
	b.Flag(root.Flags(), "db.port", "database port")
	b.Flag(root.Flags(), "timeout", "timeout")

	if got := root.Flags().Lookup("db-port").DefValue; got != "5432" {
		t.Fatalf("db default = %q", got)
	}

	if got := root.Flags().Lookup("timeout").Value.Type(); got != "duration" {
		t.Fatalf("timeout type = %q", got)
	}
}

// A root hook set before Bind still runs, once, for commands that skip loading.
func TestCobraRootHookRunsForSkippedCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("port = abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var cfg bridgetest.Config

	root := &cobra.Command{Use: bridgetest.AppName}
	hooks := 0
	root.PersistentPreRunE = func(*cobra.Command, []string) error { hooks++; return nil }
	b := stratacobra.Bind(root, &cfg, strata.WithFormats("toml"))
	root.AddCommand(b.ConfigCommand())
	root.SetOut(io.Discard)
	root.SetArgs([]string{"--config", path, "config", "set", "port", "9000"})

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	if hooks != 1 {
		t.Fatalf("hook count = %d", hooks)
	}
}

// Cobra runs only the nearest PersistentPreRunE, so a child with its own hook
// loads by calling Load.
func TestCobraLoadFromChildHook(t *testing.T) {
	var cfg bridgetest.Config

	root := &cobra.Command{Use: bridgetest.AppName}
	b := stratacobra.Bind(root, &cfg, strata.WithFormats("toml"), strata.WithEnvPrefix("CHILD_"))
	child := &cobra.Command{Use: "child", PersistentPreRunE: func(cmd *cobra.Command, _ []string) error { return b.Load(cmd) }, RunE: noop}
	b.Flag(child.Flags(), "port", "port")
	root.AddCommand(child)
	t.Setenv("CHILD_PORT", "8100")
	root.SetArgs([]string{"child", "--port", "8300"})

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	if cfg.Port != 8300 {
		t.Fatalf("port = %d", cfg.Port)
	}
}

type bindNumber int

type bindKinds struct {
	Name     string
	Enabled  bool
	Narrow   int8
	Unsigned uint16
	Ratio    float32
	Tags     []string
	Numbers  []bindNumber
	Timeout  strata.Duration
}

// Each supported field kind maps to a pflag type and accepts pflag's syntax.
func TestCobraFlagKinds(t *testing.T) {
	var cfg bindKinds

	root := &cobra.Command{Use: bridgetest.AppName, RunE: noop}

	b := stratacobra.Bind(root, &cfg, strata.WithFormats("toml"))
	for _, key := range []string{"name", "enabled", "narrow", "unsigned", "ratio", "tags", "numbers", "timeout"} {
		b.Flag(root.Flags(), key, key)
	}

	root.SetArgs([]string{"--name", "demo", "--enabled", "--narrow", "100", "--unsigned", "65000", "--ratio", "1.25", "--tags", "a,b", "--numbers", "3,4", "--timeout", "2d"})

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	if cfg.Name != "demo" || !cfg.Enabled || cfg.Narrow != 100 || cfg.Unsigned != 65000 || cfg.Ratio != 1.25 || len(cfg.Tags) != 2 || len(cfg.Numbers) != 2 || cfg.Numbers[1] != 4 || time.Duration(cfg.Timeout) != 48*time.Hour {
		t.Fatalf("config = %+v", cfg)
	}
}

// A flag bound on the root's persistent flags applies to its children.
func TestCobraPersistentFlagOnChild(t *testing.T) {
	var cfg bridgetest.Config

	root := &cobra.Command{Use: bridgetest.AppName}
	b := stratacobra.Bind(root, &cfg, strata.WithFormats("toml"))
	b.Flag(root.PersistentFlags(), "port", "port")

	child := &cobra.Command{Use: "child", RunE: noop}
	root.AddCommand(child)
	root.SetArgs([]string{"child", "--port", "9100"})

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	if cfg.Port != 9100 {
		t.Fatalf("port = %d", cfg.Port)
	}
}

// Bind keeps both kinds of root hook, which Cobra would otherwise let the
// error-returning one shadow.
func TestCobraChainsBothRootHooks(t *testing.T) {
	var cfg bridgetest.Config

	root := &cobra.Command{Use: bridgetest.AppName, RunE: noop}

	var calls []string

	root.PersistentPreRun = func(*cobra.Command, []string) { calls = append(calls, "plain") }
	root.PersistentPreRunE = func(*cobra.Command, []string) error { calls = append(calls, "error"); return nil }
	stratacobra.Bind(root, &cfg, strata.WithFormats("toml"))
	root.SetArgs(nil)

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	if strings.Join(calls, ",") != "plain,error" || cfg.Port != 8080 {
		t.Fatalf("calls = %v, port = %d", calls, cfg.Port)
	}
}

// Cobra's hidden shell-completion commands must not fail on a broken file.
func TestCobraSkipsShellCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("port = abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd} {
		t.Run(name, func(t *testing.T) {
			var cfg bridgetest.Config

			root := &cobra.Command{Use: bridgetest.AppName}
			stratacobra.Bind(root, &cfg, strata.WithPath(path), strata.WithFormats("toml"))
			root.AddCommand(&cobra.Command{Use: "serve", RunE: noop})
			root.SetOut(io.Discard)
			root.SetArgs([]string{name, "serve", "--p"})

			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Cobra adds help and completion only at the root. A deeper command that shares
// one of those names is the program's own and needs its configuration.
func TestCobraSkipsOnlyRootLevelBuiltIns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("port = 9000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var cfg bridgetest.Config

	root := &cobra.Command{Use: bridgetest.AppName}
	stratacobra.Bind(root, &cfg, strata.WithPath(path), strata.WithFormats("toml"))

	docs := &cobra.Command{Use: "docs"}
	docs.AddCommand(&cobra.Command{Use: "help", RunE: noop})
	root.AddCommand(docs)

	root.SetArgs([]string{"docs", "help"})

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	if cfg.Port != 9000 {
		t.Errorf("docs help ran with port %d, want 9000 from the config file", cfg.Port)
	}
}
