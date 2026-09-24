package strataurfave_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/zigai/strata"
	strataurfave "github.com/zigai/strata/bridge/urfave"
	"github.com/zigai/strata/internal/bridgetest"
)

type urfaveApp struct {
	*strataurfave.Binding[bridgetest.Config]

	root *cli.Command
}

func newUrfaveApp(_ *testing.T, cfg *bridgetest.Config, app bridgetest.App) bridgetest.Instance {
	options := append([]strata.Option{strata.WithFormats("toml")}, app.Options...)
	b := strataurfave.Bind(cfg, options...)
	root := &cli.Command{
		Name:                  bridgetest.AppName,
		EnableShellCompletion: true,
		Before:                b.Before,
		Flags:                 []cli.Flag{b.ConfigFlag()},
		ErrWriter:             io.Discard,
	}

	for _, name := range slices.Sorted(maps.Keys(app.Commands)) {
		cmd := &cli.Command{Name: name, Action: noop}
		for _, flag := range app.Commands[name] {
			var aliases []string
			if flag.Short != "" {
				aliases = []string{flag.Short}
			}

			cmd.Flags = append(cmd.Flags, b.Flag(flag.Key, flag.Key, aliases...))
		}

		root.Commands = append(root.Commands, cmd)
	}

	ping := &cli.Command{Name: "ping", Flags: []cli.Flag{&cli.StringFlag{Name: "port"}}, Action: noop}
	root.Commands = append(root.Commands, ping, b.ConfigCommand())

	return &urfaveApp{Binding: b, root: root}
}

func (a *urfaveApp) Run(args ...string) (string, error) {
	var out bytes.Buffer

	a.root.Writer = &out
	err := a.root.Run(context.Background(), append([]string{bridgetest.AppName}, args...))

	return out.String(), err
}

func noop(context.Context, *cli.Command) error { return nil }

func TestUrfaveSharedBehavior(t *testing.T) {
	bridgetest.Run(t, newUrfaveApp)
}

func TestUrfaveRequiresFormatsOnRun(t *testing.T) {
	var cfg bridgetest.Config

	b := strataurfave.Bind(&cfg)
	root := &cli.Command{Name: bridgetest.AppName, Before: b.Before, Action: noop}

	if err := root.Run(context.Background(), []string{bridgetest.AppName}); !errors.Is(err, strata.ErrNoFormats) {
		t.Fatalf("Run error = %v, want ErrNoFormats", err)
	}
}

type urfaveKinds struct {
	Enabled bool
	Port    int
	Tags    []string
	Timeout strata.Duration
}

// Each supported field kind maps to a urfave/cli flag and accepts its syntax,
// including repeated flags for a list.
func TestUrfaveFlagKinds(t *testing.T) {
	var cfg urfaveKinds

	b := strataurfave.Bind(&cfg, strata.WithFormats("toml"))

	cmd := &cli.Command{Name: bridgetest.AppName, Before: b.Before, Flags: []cli.Flag{
		b.Flag("enabled", "enabled"), b.Flag("port", "port", "p"), b.Flag("tags", "tags"), b.Flag("timeout", "timeout"),
	}, Action: noop}
	if err := cmd.Run(context.Background(), []string{bridgetest.AppName, "--enabled", "-p", "9000", "--tags", "a", "--tags", "b", "--timeout", "2d"}); err != nil {
		t.Fatal(err)
	}

	if !cfg.Enabled || cfg.Port != 9000 || len(cfg.Tags) != 2 || time.Duration(cfg.Timeout) != 48*time.Hour {
		t.Fatalf("config = %+v", cfg)
	}
}

// A shell completion request runs Before, and must not fail on a broken file.
// urfave/cli only exposes the request through the process arguments.
func TestUrfaveShellCompletionSkipsLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("port = abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	args := []string{bridgetest.AppName, "serve", "--generate-shell-completion"}
	original := os.Args
	os.Args = args

	t.Cleanup(func() { os.Args = original })

	var cfg bridgetest.Config

	b := strataurfave.Bind(&cfg, strata.WithPath(path), strata.WithFormats("toml"))
	root := &cli.Command{
		Name:                  bridgetest.AppName,
		EnableShellCompletion: true,
		Before:                b.Before,
		Writer:                io.Discard,
		ErrWriter:             io.Discard,
		Commands:              []*cli.Command{{Name: "serve", Action: noop}},
	}

	if err := root.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
}
