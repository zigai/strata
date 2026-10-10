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
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/zigai/strata"
	strataurfave "github.com/zigai/strata/bridge/urfave"
	"github.com/zigai/strata/internal/bridgetest"
)

type urfaveApp struct {
	*strataurfave.Binding[bridgetest.Config]

	root   *cli.Command
	stderr bytes.Buffer
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

	a.stderr.Reset()
	a.root.Writer = &out
	a.root.ErrWriter = &a.stderr
	err := a.root.Run(context.Background(), append([]string{bridgetest.AppName}, args...))

	return out.String(), err
}

func (a *urfaveApp) Stderr() string { return a.stderr.String() }

func noop(context.Context, *cli.Command) error { return nil }

func TestUrfaveSharedBehavior(t *testing.T) {
	bridgetest.Run(t, newUrfaveApp)
}

type showSecretNode struct {
	Token strata.Secret
	Next  *showSecretNode
}

func TestUrfaveShowRedactsSecretCollections(t *testing.T) {
	t.Parallel()

	type config struct {
		Credentials map[string]string `strata:"credentials,secret"`
		Tokens      []strata.Secret
		Node        *showSecretNode
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("tokens = ['private-value']\n[credentials]\nservice = 'private-value'\n[node.next]\ntoken = 'private-value'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var (
		cfg    config
		output bytes.Buffer
	)

	binding := strataurfave.Bind(&cfg, strata.WithPath(path), strata.WithFormats("toml"))

	root := &cli.Command{Name: bridgetest.AppName, Before: binding.Before, Writer: &output, ErrWriter: io.Discard, Commands: []*cli.Command{binding.ConfigCommand()}}
	if err := root.Run(t.Context(), []string{bridgetest.AppName, "config", "show"}); err != nil {
		t.Fatal(err)
	}

	if cfg.Node == nil || cfg.Node.Next == nil || cfg.Node.Next.Token.Value() != "private-value" {
		t.Fatalf("secret did not load: %+v", cfg)
	}

	if text := output.String(); strings.Contains(text, "private-value") || !strings.Contains(text, "credentials.service") || !strings.Contains(text, "node.next.token") || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("show output = %q", text)
	}
}

func TestUrfaveSecretCollectionFlagPanics(t *testing.T) {
	t.Parallel()

	type config struct {
		Tokens []strata.Secret `strata:"tokens"`
	}

	cfg := config{Tokens: []strata.Secret{"private-value"}}
	binding := strataurfave.Bind(&cfg, strata.WithFormats("toml"))

	defer func() {
		if recover() == nil {
			t.Fatal("binding a secret collection flag did not panic")
		}
	}()

	binding.Flag("tokens", "Tokens")
}

func TestUrfaveInheritedFlagsBetweenCommands(t *testing.T) {
	for _, args := range [][]string{
		{"group", "--config", "FILE", "serve", "--port", "9001"},
		{"group", "-c", "FILE", "serve", "--port", "9001"},
		{"group", "serve", "--config", "FILE", "--port", "9001"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("port = 8101\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			argv := slices.Clone(args)
			for index, argument := range argv {
				if argument == "FILE" {
					argv[index] = path
				}
			}

			var cfg bridgetest.Config

			binding := strataurfave.Bind(&cfg, strata.WithFormats("toml"))

			var actionPort int

			leaf := &cli.Command{Name: "serve", Flags: []cli.Flag{binding.Flag("port", "Port")}, Action: func(_ context.Context, _ *cli.Command) error {
				actionPort = cfg.Port
				return nil
			}}

			root := &cli.Command{Name: bridgetest.AppName, Before: binding.Before, Flags: []cli.Flag{binding.ConfigFlag()}, Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{{Name: "group", Commands: []*cli.Command{leaf}}}}
			if err := root.Run(t.Context(), append([]string{bridgetest.AppName}, argv...)); err != nil {
				t.Fatal(err)
			}

			origin, found := binding.Metadata().Where("port")
			if actionPort != 9001 || !found || origin.Source != strata.SourceFlag || origin.RawValue != "9001" {
				t.Fatalf("action port = %d, origin = %+v, %t", actionPort, origin, found)
			}
		})
	}
}

func TestUrfaveRepairWithInheritedFlagBetweenCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("port = invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var cfg bridgetest.Config

	binding := strataurfave.Bind(&cfg, strata.WithFormats("toml"))

	var output bytes.Buffer

	root := &cli.Command{Name: bridgetest.AppName, Before: binding.Before, Flags: []cli.Flag{binding.ConfigFlag()}, Writer: &output, ErrWriter: io.Discard, Commands: []*cli.Command{binding.ConfigCommand()}}
	if err := root.Run(t.Context(), []string{bridgetest.AppName, "config", "--config", path, "path"}); err != nil {
		t.Fatal(err)
	}

	if output.String() != path+"\n" || binding.Metadata() != nil {
		t.Fatalf("output = %q, metadata = %+v", output.String(), binding.Metadata())
	}
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
