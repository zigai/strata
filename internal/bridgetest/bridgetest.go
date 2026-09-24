// Package bridgetest holds the behavior every CLI bridge shares, as scenarios
// that each bridge's tests run through a small adapter for its CLI library.
//
// Behavior that belongs to one library, such as Cobra's hook chaining or
// urfave/cli's shell-completion flag, stays in that bridge's own tests.
package bridgetest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"
)

// AppName is the root command name of every app a scenario builds.
const AppName = "app"

// Database is the nested section of [Config].
type Database struct {
	Port int
}

// Config is the configuration every scenario binds.
type Config struct {
	Port    int
	Timeout strata.Duration
	Verbose bool
	Narrow  int8
	DB      Database
	Token   strata.Secret
}

// SetDefaults gives the non-zero defaults the scenarios assert on.
func (c *Config) SetDefaults() {
	c.Port = 8080
	c.Timeout = strata.Duration(30 * time.Second)
	c.DB.Port = 5432
}

// Flag is one bound flag: a configuration key and an optional shorthand.
type Flag struct {
	Key   string
	Short string
}

// App describes the CLI a scenario runs. Besides the commands listed here, each
// app has the bridge's config command, a -c/--config flag, and a "ping"
// command with its own unbound string --port flag.
type App struct {
	Options []strata.Option

	// Commands maps each command under the root to the keys it binds as flags.
	Commands map[string][]Flag
}

// Instance is one app built by a bridge. Run may be called more than once.
type Instance interface {
	// Run executes the app with args, which exclude the program name, and
	// returns what the app wrote to standard output.
	Run(args ...string) (string, error)
	Metadata() *strata.Metadata
	Path() (string, error)
	Init() (string, error)
}

// Bridge builds an app with one CLI library, bound to cfg.
type Bridge func(t *testing.T, cfg *Config, app App) Instance

// Run runs every shared scenario against bridge. The scenarios set environment
// variables, so they do not run in parallel.
func Run(t *testing.T, bridge Bridge) {
	t.Helper()

	for _, scenario := range []struct {
		name string
		run  func(*testing.T, Bridge)
	}{
		{"LayersFlagsAndShow", layersFlagsAndShow},
		{"UnrelatedFlagWithTheSameName", unrelatedFlagWithTheSameName},
		{"EditsSkipLoadingABrokenFile", editsSkipLoadingABrokenFile},
		{"BuiltInsSkipLoadingABrokenFile", builtInsSkipLoadingABrokenFile},
		{"UserCommandsNamedLikeEditsLoad", userCommandsNamedLikeEditsLoad},
		{"DefaultAppNameMatchesEditPath", defaultAppNameMatchesEditPath},
		{"EditPathUsesExplicitLoadingPath", editPathUsesExplicitLoadingPath},
		{"SecretKeyCannotBeAFlag", secretKeyCannotBeAFlag},
		{"FlagValueMustFitTheField", flagValueMustFitTheField},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Keep real system and user files for "app" out of every scenario.
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_DIRS", t.TempDir())
			t.Setenv("APPDATA", t.TempDir())
			t.Setenv("ProgramData", t.TempDir())

			scenario.run(t, bridge)
		})
	}
}

// Defaults, files, environment, and flags stack in that order; only flags the
// user typed override, and `config show` reports where each value came from.
func layersFlagsAndShow(t *testing.T, bridge Bridge) {
	path := writeConfig(t, "port = 8100\n[db]\nport = 6000\n")
	t.Setenv("APP_PORT", "8200")

	var cfg Config

	app := bridge(t, &cfg, App{
		Options:  []strata.Option{strata.WithPath(path), strata.WithEnvPrefix("APP_")},
		Commands: map[string][]Flag{"serve": {{Key: "port", Short: "p"}, {Key: "db.port"}, {Key: "timeout"}}},
	})

	mustRun(t, app, "serve")

	if cfg.Port != 8200 || cfg.DB.Port != 6000 {
		t.Fatalf("without flags: port = %d, db.port = %d, want 8200 from env and 6000 from the file", cfg.Port, cfg.DB.Port)
	}

	expectSource(t, app, "port", strata.SourceEnv)
	expectSource(t, app, "db.port", strata.SourceFile)

	if origin, _ := app.Metadata().Where("verbose"); origin.RawValue != "false" {
		t.Fatalf("verbose origin = %+v, want the zero default recorded", origin)
	}

	mustRun(t, app, "serve", "-p", "9000", "--db-port", "7000", "--timeout", "1d")

	if cfg.Port != 9000 || cfg.DB.Port != 7000 || time.Duration(cfg.Timeout) != 24*time.Hour {
		t.Fatalf("with flags: config = %+v", cfg)
	}

	expectSource(t, app, "port", strata.SourceFlag)
	expectSource(t, app, "db.port", strata.SourceFlag)

	out := mustRun(t, app, "config", "show")

	for _, row := range []string{`port\s+8200\s+env APP_PORT`, `db\.port\s+6000\s+file ` + regexp.QuoteMeta(path), `token\s+\(unset\)\s+default`} {
		if !regexp.MustCompile(row).MatchString(out) {
			t.Errorf("config show lacks a row matching %q:\n%s", row, out)
		}
	}
}

// A flag another command declares under a bound flag's name is not bound, and
// the same key can be bound on several commands.
func unrelatedFlagWithTheSameName(t *testing.T, bridge Bridge) {
	var cfg Config

	app := bridge(t, &cfg, App{
		Options:  nil,
		Commands: map[string][]Flag{"serve": {{Key: "port"}}, "check": {{Key: "port"}}},
	})

	mustRun(t, app, "ping", "--port", "22")

	if cfg.Port != 8080 {
		t.Fatalf("ping --port changed port to %d", cfg.Port)
	}

	expectSource(t, app, "port", strata.SourceDefault)

	for _, test := range []struct {
		command, value string
		want           int
	}{
		{"check", "9000", 9000},
		{"serve", "9100", 9100},
	} {
		mustRun(t, app, test.command, "--port", test.value)

		if cfg.Port != test.want {
			t.Fatalf("%s --port %s: port = %d", test.command, test.value, cfg.Port)
		}
	}
}

// The edit commands run without loading, so they can repair a broken file, and
// setting a secret does not echo its value.
func editsSkipLoadingABrokenFile(t *testing.T, bridge Bridge) {
	path := writeConfig(t, "port = abc\n")

	var cfg Config

	app := bridge(t, &cfg, App{Options: nil, Commands: map[string][]Flag{"serve": nil}})

	if out := mustRun(t, app, "--config", path, "config", "path"); strings.TrimSpace(out) != path {
		t.Fatalf("config path printed %q, want %q", out, path)
	}

	mustRun(t, app, "--config", path, "config", "set", "port", "9000")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), "9000") {
		t.Fatalf("edited file = %q", data)
	}

	if out := mustRun(t, app, "--config", path, "config", "set", "token", "private-value"); strings.Contains(out, "private-value") {
		t.Fatalf("config set echoed the secret: %q", out)
	}

	mustRun(t, app, "--config", path, "serve")

	if cfg.Port != 9000 || cfg.Token.Value() != "private-value" {
		t.Fatalf("repaired file loaded port %d, token set %t", cfg.Port, cfg.Token.Value() != "")
	}
}

// The library's own help and completion commands need no configuration.
func builtInsSkipLoadingABrokenFile(t *testing.T, bridge Bridge) {
	path := writeConfig(t, "port = abc\n")

	var cfg Config

	app := bridge(t, &cfg, App{Options: []strata.Option{strata.WithPath(path)}, Commands: map[string][]Flag{"serve": nil}})

	for _, args := range [][]string{{"help"}, {"completion", "bash"}} {
		mustRun(t, app, args...)
	}

	if _, err := app.Run("serve"); err == nil {
		t.Fatal("serve loaded a broken file without error")
	}
}

// A program's own commands named like the edit subcommands still load.
func userCommandsNamedLikeEditsLoad(t *testing.T, bridge Bridge) {
	var cfg Config

	app := bridge(t, &cfg, App{Options: nil, Commands: map[string][]Flag{"set": nil, "init": nil, "path": nil}})

	for _, name := range []string{"set", "init", "path"} {
		cfg.Port = 0

		mustRun(t, app, name)

		if cfg.Port != 8080 {
			t.Fatalf("user %s command ran without defaults: port = %d", name, cfg.Port)
		}
	}
}

// Without WithAppName, the root command's name picks the user file, and the
// file `config init` writes is the one the next load reads.
func defaultAppNameMatchesEditPath(t *testing.T, bridge Bridge) {
	var cfg Config

	app := bridge(t, &cfg, App{Options: nil, Commands: map[string][]Flag{"serve": nil}})

	mustRun(t, app, "config", "init")

	path, err := app.Path()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(path, filepath.Join(AppName, "config.toml")) {
		t.Fatalf("edit path = %s", path)
	}

	mustRun(t, app, "serve")

	if files := app.Metadata().ActiveFiles(); len(files) != 1 || files[0] != path {
		t.Fatalf("active files = %v, want [%s]", files, path)
	}
}

// A WithPath passed to Bind is also where edits go.
func editPathUsesExplicitLoadingPath(t *testing.T, bridge Bridge) {
	path := filepath.Join(t.TempDir(), "config.toml")

	var cfg Config

	app := bridge(t, &cfg, App{Options: []strata.Option{strata.WithPath(path)}, Commands: nil})

	got, err := app.Path()
	if err != nil || got != path {
		t.Fatalf("edit path = %q, %v, want %q", got, err, path)
	}
}

// A secret never becomes a flag: its value would land in shell history.
func secretKeyCannotBeAFlag(t *testing.T, bridge Bridge) {
	defer func() {
		if recover() == nil {
			t.Fatal("binding a secret key as a flag did not panic")
		}
	}()

	var cfg Config

	bridge(t, &cfg, App{Options: nil, Commands: map[string][]Flag{"serve": {{Key: "token"}}}})
}

// A flag value that does not fit the field is an error, not a wrapped value.
func flagValueMustFitTheField(t *testing.T, bridge Bridge) {
	var cfg Config

	app := bridge(t, &cfg, App{Options: nil, Commands: map[string][]Flag{"serve": {{Key: "narrow"}}}})

	if _, err := app.Run("serve", "--narrow", "200"); err == nil {
		t.Fatalf("--narrow 200 was accepted: narrow = %d", cfg.Narrow)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func mustRun(t *testing.T, app Instance, args ...string) string {
	t.Helper()

	out, err := app.Run(args...)
	if err != nil {
		t.Fatalf("%s %s: %v", AppName, strings.Join(args, " "), err)
	}

	return out
}

func expectSource(t *testing.T, app Instance, key string, want strata.SourceKind) {
	t.Helper()

	if origin, _ := app.Metadata().Where(key); origin.Source != want {
		t.Fatalf("%s origin = %+v, want source %s", key, origin, want)
	}
}
