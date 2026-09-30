package bridgetest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"
)

const AppName = "app"

type Database struct {
	Port int
}

type Config struct {
	Port    int
	Timeout strata.Duration
	Verbose bool
	Narrow  int8
	DB      Database
	Token   strata.Secret
}

func (c *Config) SetDefaults() {
	c.Port = 8080
	c.Timeout = strata.Duration(30 * time.Second)
	c.DB.Port = 5432
}

type Flag struct {
	Key   string
	Short string
}

type App struct {
	Options []strata.Option

	Commands map[string][]Flag
}

type Instance interface {
	// args excludes the program name.
	Run(args ...string) (string, error)

	Stderr() string
	Metadata() *strata.Metadata
	EditPath() (string, error)
	Init() (string, error)
}

type Bridge func(t *testing.T, cfg *Config, app App) Instance

func Run(t *testing.T, bridge Bridge) {
	t.Helper()

	// The scenarios set environment variables, so they do not run in parallel.
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
		{"ShowListsKeysInOrderAndWarns", showListsKeysInOrderAndWarns},
		{"SetReportsThePathAndNeedsAFile", setReportsThePathAndNeedsAFile},
		{"HelpShowsTheDefault", helpShowsTheDefault},
		{"FlagSetupPanicsOnBadKeys", flagSetupPanicsOnBadKeys},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_DIRS", t.TempDir())
			t.Setenv("APPDATA", t.TempDir())
			t.Setenv("ProgramData", t.TempDir())

			scenario.run(t, bridge)
		})
	}
}

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

func defaultAppNameMatchesEditPath(t *testing.T, bridge Bridge) {
	var cfg Config

	app := bridge(t, &cfg, App{Options: nil, Commands: map[string][]Flag{"serve": nil}})

	mustRun(t, app, "config", "init")

	path, err := app.EditPath()
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

func editPathUsesExplicitLoadingPath(t *testing.T, bridge Bridge) {
	path := filepath.Join(t.TempDir(), "config.toml")

	var cfg Config

	app := bridge(t, &cfg, App{Options: []strata.Option{strata.WithPath(path)}, Commands: nil})

	got, err := app.EditPath()
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

func flagValueMustFitTheField(t *testing.T, bridge Bridge) {
	var cfg Config

	app := bridge(t, &cfg, App{Options: nil, Commands: map[string][]Flag{"serve": {{Key: "narrow"}}}})

	if _, err := app.Run("serve", "--narrow", "200"); err == nil {
		t.Fatalf("--narrow 200 was accepted: narrow = %d", cfg.Narrow)
	}
}

func showListsKeysInOrderAndWarns(t *testing.T, bridge Bridge) {
	path := writeConfig(t, "prot = 1\nport = 8100\ntoken = 'hunter2-secret'\n")

	var cfg Config

	app := bridge(t, &cfg, App{Options: []strata.Option{strata.WithPath(path)}, Commands: nil})

	out := mustRun(t, app, "config", "show")

	var rows [][]string
	for line := range strings.Lines(out) {
		rows = append(rows, strings.Fields(line))
	}

	want := [][]string{
		{"KEY", "VALUE", "SOURCE"},
		{"port", "8100", "file", path},
		{"timeout", "30s", "default"},
		{"verbose", "false", "default"},
		{"narrow", "0", "default"},
		{"db.port", "5432", "default"},
		{"token", "[REDACTED]", "file", path},
	}
	if !slices.EqualFunc(rows, want, slices.Equal) {
		t.Fatalf("config show printed:\n%s\nwant rows %q", out, want)
	}

	if strings.Contains(out, "hunter2") {
		t.Fatalf("config show printed the secret:\n%s", out)
	}

	if got, want := app.Stderr(), "warning: "+path+": unknown key \"prot\" (did you mean \"port\"?)\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func setReportsThePathAndNeedsAFile(t *testing.T, bridge Bridge) {
	path := writeConfig(t, "port = 8100\n")

	var cfg Config

	app := bridge(t, &cfg, App{Options: []strata.Option{strata.WithPath(path)}, Commands: nil})

	if out := mustRun(t, app, "config", "set", "port", "9000"); out != path+": set port\n" {
		t.Fatalf("config set printed %q, want %q", out, path+": set port\n")
	}

	missing := filepath.Join(t.TempDir(), "config.toml")
	app = bridge(t, &cfg, App{Options: []strata.Option{strata.WithPath(missing)}, Commands: nil})

	_, err := app.Run("config", "set", "port", "9000")
	if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "config init") {
		t.Fatalf("err = %v, want fs.ErrNotExist and a hint to run config init", err)
	}

	if _, statErr := os.Stat(missing); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("config set created %s", missing)
	}
}

func helpShowsTheDefault(t *testing.T, bridge Bridge) {
	var cfg Config

	app := bridge(t, &cfg, App{Options: nil, Commands: map[string][]Flag{"serve": {{Key: "db.port"}}}})

	out := mustRun(t, app, "serve", "--help")
	if !regexp.MustCompile(`--db-port[^\n]*5432`).MatchString(out) {
		t.Fatalf("serve --help does not show the default 5432 for --db-port:\n%s", out)
	}
}

func flagSetupPanicsOnBadKeys(t *testing.T, bridge Bridge) {
	for _, key := range []string{"prot", "db"} {
		t.Run(key, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("binding %q as a flag did not panic", key)
				}
			}()

			var cfg Config

			bridge(t, &cfg, App{Options: nil, Commands: map[string][]Flag{"serve": {{Key: key}}}})
		})
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
