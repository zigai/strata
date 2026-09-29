package strata_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"
)

type yamlMergeConfig struct {
	Database struct {
		Host string `strata:"dbHost"`
	}
}

type panickyConfig struct {
	Port int `strata:"port"`
}

func (p *panickyConfig) SetDefaults() {
	panic("defaults exploded")
}

type nestedPanicky struct {
	Inner panickyConfig `strata:"inner"`
}

func TestLoadInto(t *testing.T) {
	t.Parallel()

	filePath := writeFile(t, "load_into.yaml", "app_name: into-app\nmax_retries: 42\n")

	var target cascadingConfig

	meta, err := strata.LoadInto(&target, strata.WithPath(filePath), strata.WithFormats("yaml"))
	if err != nil {
		t.Fatalf("LoadInto error: %v", err)
	}

	if target.AppName != "into-app" || target.MaxRetries != 42 {
		t.Fatalf("target = %+v, want into-app and 42", target)
	}

	if len(meta.ActiveFiles()) != 1 {
		t.Fatalf("ActiveFiles = %v, want 1 file", meta.ActiveFiles())
	}
}

func TestLoadIntoWithDefaults(t *testing.T) {
	t.Parallel()

	type customDefaults struct {
		Host string `strata:"host"`
		Port int    `strata:"port"`
	}

	seed := customDefaults{
		Host: "custom-host",
		Port: 9999,
	}

	var target customDefaults

	_, err := strata.LoadInto(&target, strata.WithoutFiles(), strata.WithDefaults(seed))
	if err != nil {
		t.Fatalf("LoadInto error: %v", err)
	}

	if target.Host != "custom-host" || target.Port != 9999 {
		t.Fatalf("target = %+v, want custom-host:9999", target)
	}
}

func TestPrecedenceCascading(t *testing.T) {
	isolateTiers(t)

	projectFile := writeFile(t, "precedence.toml", "app_name = \"project-app\"\nmax_retries = 10\n")

	t.Setenv("PREC_APP_NAME", "env-app")

	cfg, meta, err := strata.LoadWithMetadata[cascadingConfig](
		strata.WithPath(projectFile),
		strata.WithAppName("precedence"),
		strata.WithFormats("toml"),
		strata.WithEnvPrefix("PREC_"),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.AppName != "env-app" {
		t.Errorf("AppName = %q, want env-app", cfg.AppName)
	}

	if cfg.MaxRetries != 10 {
		t.Errorf("MaxRetries = %d, want 10", cfg.MaxRetries)
	}

	if cfg.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", cfg.Timeout)
	}

	if orig, ok := meta.Where("app_name"); !ok || orig.Source != strata.SourceEnv {
		t.Errorf("app_name origin = %+v, want SourceEnv", orig)
	}

	if orig, ok := meta.Where("max_retries"); !ok || orig.Source != strata.SourceFile {
		t.Errorf("max_retries origin = %+v, want SourceFile", orig)
	}

	if orig, ok := meta.Where("timeout"); !ok || orig.Source != strata.SourceDefault {
		t.Errorf("timeout origin = %+v, want SourceDefault", orig)
	}
}

func TestBooleanFalseOverwriteDefense(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sparse.toml")
	content := []byte("app_name = \"sparse-app\"\nmax_retries = 5\n")

	if err := os.WriteFile(filePath, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, meta, err := strata.LoadWithMetadata[cascadingConfig](
		strata.WithPath(filePath),
		strata.WithFormats("toml"),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.AppName != "sparse-app" {
		t.Errorf("AppName = %q, want sparse-app", cfg.AppName)
	}

	if cfg.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", cfg.MaxRetries)
	}

	if !cfg.AutoClean {
		t.Errorf("AutoClean = false, want true (Boolean false-overwrite defense failed!)")
	}

	if orig, ok := meta.Where("auto_clean"); !ok || orig.Source != strata.SourceDefault {
		t.Errorf("auto_clean origin = %+v, want SourceDefault", orig)
	}

	if orig, ok := meta.Where("max_retries"); !ok || orig.Source != strata.SourceFile {
		t.Errorf("max_retries origin = %+v, want SourceFile", orig)
	}
}

func TestSliceReplacementSemantics(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "replace_slice.toml")
	content := []byte("ignore_paths = [\"/custom/build\"]\n")

	if err := os.WriteFile(filePath, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := strata.Load[cascadingConfig](
		strata.WithPath(filePath),
		strata.WithFormats("toml"),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	wantPaths := []string{"/custom/build"}
	if !reflect.DeepEqual(cfg.IgnorePaths, wantPaths) {
		t.Fatalf("IgnorePaths = %v, want %v (Slice replacement failed!)", cfg.IgnorePaths, wantPaths)
	}
}

func TestExplicitPathOutranksWithoutFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cfg.toml")
	if err := os.WriteFile(path, []byte("small = 42\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := strata.Load[narrowConfig](strata.WithPath(path), strata.WithoutFiles(), strata.WithFormats("toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Small != 42 {
		t.Fatalf("Small = %d, want 42", got.Small)
	}
}

func TestMaxFileSizeOption(t *testing.T) {
	t.Parallel()

	type Cfg struct {
		Data string `strata:"data"`
	}

	buf := bytes.NewBufferString(strings.Repeat("a", 200))

	_, err := strata.Load[Cfg](
		strata.WithPath("-"),
		strata.WithStdin(buf),
		strata.WithFormats("toml"),
		strata.WithMaxFileSize(100),
	)
	if err == nil {
		t.Fatalf("expected error for exceeding max file size, got nil")
	}
}

func TestMaxFileSizeAcceptsMaxInt64(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cfg.toml")
	if err := os.WriteFile(path, []byte("small = 7\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := strata.Load[narrowConfig](strata.WithPath(path), strata.WithMaxFileSize(math.MaxInt64), strata.WithFormats("toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Small != 7 {
		t.Fatalf("Small = %d, want 7", got.Small)
	}
}

func TestFileTooLargeIsClassifiable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cfg.toml")
	if err := os.WriteFile(path, []byte("small = 7\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := strata.Load[narrowConfig](strata.WithPath(path), strata.WithMaxFileSize(4), strata.WithFormats("toml"))
	if !errors.Is(err, strata.ErrFileTooLarge) {
		t.Fatalf("err = %v, want it to wrap ErrFileTooLarge", err)
	}
}

func TestPanickingDefaultsIsReported(t *testing.T) {
	t.Parallel()

	_, err := strata.Load[panickyConfig](strata.WithoutFiles())
	if err == nil || !strings.Contains(err.Error(), "defaults exploded") {
		t.Fatalf("top-level err = %v, want the panic reported", err)
	}

	if _, err := strata.Load[nestedPanicky](strata.WithoutFiles()); err == nil {
		t.Fatal("nested panicking SetDefaults returned no error")
	} else if !strings.Contains(err.Error(), "defaults exploded") {
		t.Errorf("err = %v, want it to carry the panicking value", err)
	}
}

func TestUserTierDotConfigEndToEnd(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("skipping Unix/macOS user tier test on Windows")
	}

	homeDir := t.TempDir()

	appConfigDir := filepath.Join(homeDir, ".config", "myapp")
	if err := os.MkdirAll(appConfigDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	configFile := filepath.Join(appConfigDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("port = 8181\nhost = \"dot-config-host\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	isolateTiers(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", homeDir)

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithFormats("toml"),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != 8181 {
		t.Errorf("cfg.Port = %d, want 8181", cfg.Port)
	}

	if cfg.Host != "dot-config-host" {
		t.Errorf("cfg.Host = %q, want dot-config-host", cfg.Host)
	}

	if orig, ok := meta.Where(portKey); !ok || orig.Source != strata.SourceUser {
		t.Errorf("port origin = %+v, want SourceUser", orig)
	}
}

func TestUntaggedStructResolution(t *testing.T) {
	type ServerConfig struct {
		Host string
		Port int
	}

	dir := t.TempDir()

	tomlPath := filepath.Join(dir, "config.toml")

	tomlContent := []byte("host = \"10.0.0.1\"\nport = 9000\n")
	if err := os.WriteFile(tomlPath, tomlContent, 0o600); err != nil {
		t.Fatal(err)
	}

	isolateTiers(t)
	t.Setenv("TESTUNTAGGED_PORT", "9999")

	cfg, meta, err := strata.LoadWithMetadata[ServerConfig](
		strata.WithPath(tomlPath),
		strata.WithAppName("myapp"),
		strata.WithFormats("toml"),
		strata.WithEnvPrefix("TESTUNTAGGED_"),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Host != "10.0.0.1" || cfg.Port != 9999 {
		t.Errorf("got Host=%q, Port=%d; want Host=10.0.0.1, Port=9999", cfg.Host, cfg.Port)
	}

	origin, ok := meta.Where(portKey)
	if !ok {
		t.Fatal("expected origin for port")
	}

	if origin.Source != strata.SourceEnv {
		t.Errorf("origin.Source = %v, want %v", origin.Source, strata.SourceEnv)
	}
}

func TestYAMLMergeLoadsValueAndOrigin(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{name: "alias", data: "base: &base\n  dbHost: merged.example\ndatabase:\n  <<: *base\n", want: "merged.example"},
		{name: "inline", data: "database: {<<: {dbHost: inline.example}}\n", want: "inline.example"},
		{name: "sequence", data: "first: &first {dbHost: first.example}\nsecond: &second {dbHost: second.example}\ndatabase:\n  <<: [*first, *second]\n", want: "first.example"},
		{name: "override", data: "base: &base {dbHost: merged.example}\ndatabase:\n  <<: *base\n  dbHost: explicit.example\n", want: "explicit.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, meta, err := strata.LoadWithMetadata[yamlMergeConfig](strata.WithPath(path), strata.WithFormats("yaml"))
			if err != nil {
				t.Fatal(err)
			}

			if cfg.Database.Host != tc.want {
				t.Fatalf("merged host = %q", cfg.Database.Host)
			}

			origin, ok := meta.Where("database.dbHost")
			if !ok || origin.Source != strata.SourceFile || origin.RawValue != tc.want {
				t.Fatalf("merged origin = %+v, %t", origin, ok)
			}
		})
	}
}

func TestOptionalPath(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "absent.toml")

	cfg, err := strata.Load[typoConfig](strata.WithOptionalPath(missing), strata.WithFormats("toml"))
	if err != nil || cfg.Port != 8080 {
		t.Fatalf("optional missing file: cfg=%+v err=%v, want defaults", cfg, err)
	}

	if _, err := strata.Load[typoConfig](strata.WithPath(missing), strata.WithFormats("toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("required missing file: err = %v, want os.ErrNotExist", err)
	}

	present := writeFile(t, "config.toml", "port = 9000\n")

	cfg, err = strata.Load[typoConfig](strata.WithOptionalPath(present), strata.WithFormats("toml"))
	if err != nil || cfg.Port != 9000 {
		t.Fatalf("optional present file: cfg=%+v err=%v, want port 9000", cfg, err)
	}

	broken := writeFile(t, "broken.toml", "port = = 1\n")
	if _, err := strata.Load[typoConfig](strata.WithOptionalPath(broken), strata.WithFormats("toml")); !errors.Is(err, strata.ErrMalformed) {
		t.Fatalf("optional malformed file: err = %v, want ErrMalformed", err)
	}
}

func TestWithDefaultsOverridesSetDefaults(t *testing.T) {
	t.Parallel()

	cfg, meta, err := strata.LoadWithMetadata[typoConfig](strata.WithDefaults(typoConfig{Host: "example", Port: 7000}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Host != "example" || cfg.Port != 7000 {
		t.Fatalf("cfg = %+v, want WithDefaults to win over SetDefaults", cfg)
	}

	if origin, ok := meta.Where(portKey); !ok || origin.Source != strata.SourceDefault || origin.RawValue != "7000" {
		t.Fatalf("port origin = %+v, want default 7000", origin)
	}
}

func TestWithDefaultsTypeMismatch(t *testing.T) {
	t.Parallel()

	_, err := strata.Load[typoConfig](strata.WithDefaults(demoConfig{}))
	if !errors.Is(err, strata.ErrDefaultsTypeMismatch) {
		t.Fatalf("err = %v, want ErrDefaultsTypeMismatch", err)
	}
}

type tierConfig struct {
	Port int    `strata:"port"`
	Note string `strata:"note"`
}

func (c *tierConfig) SetDefaults() {
	c.Port = 1
	c.Note = "default"
}

// Every subset of the four layers sets port to its own value; the highest
// layer present wins. note is set only by the system file and survives
// whatever sits above it.
func TestLayerPrecedenceAcrossEveryTierSubset(t *testing.T) {
	type tiers struct{ system, user, file, env bool }

	for _, tc := range []struct {
		tiers    tiers
		wantPort int
		wantNote string
	}{
		{tiers{}, 1, "default"},
		{tiers{system: true}, 2, "system"},
		{tiers{user: true}, 3, "default"},
		{tiers{file: true}, 4, "default"},
		{tiers{env: true}, 5, "default"},
		{tiers{system: true, user: true}, 3, "system"},
		{tiers{system: true, file: true}, 4, "system"},
		{tiers{system: true, env: true}, 5, "system"},
		{tiers{user: true, file: true}, 4, "default"},
		{tiers{user: true, env: true}, 5, "default"},
		{tiers{file: true, env: true}, 5, "default"},
		{tiers{system: true, user: true, file: true}, 4, "system"},
		{tiers{system: true, user: true, env: true}, 5, "system"},
		{tiers{system: true, file: true, env: true}, 5, "system"},
		{tiers{user: true, file: true, env: true}, 5, "default"},
		{tiers{system: true, user: true, file: true, env: true}, 5, "system"},
	} {
		t.Run(fmt.Sprintf("%+v", tc.tiers), func(t *testing.T) {
			systemBase, userBase := isolateTiers(t)

			opts := []strata.Option{strata.WithAppName("myapp"), strata.WithFormats("toml"), strata.WithEnvPrefix("MYAPP_")}

			if tc.tiers.system {
				writeTierFile(t, systemBase, "port = 2\nnote = \"system\"\n")
			}

			if tc.tiers.user {
				writeTierFile(t, userBase, "port = 3\n")
			}

			if tc.tiers.file {
				opts = append(opts, strata.WithPath(writeFile(t, "explicit.toml", "port = 4\n")))
			}

			if tc.tiers.env {
				t.Setenv("MYAPP_PORT", "5")
			}

			got, err := strata.Load[tierConfig](opts...)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			want := tierConfig{Port: tc.wantPort, Note: tc.wantNote}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

// LoadInto starts from the value the caller seeded: a key no layer mentions
// keeps the seeded value instead of being zeroed.
func TestLoadIntoKeepsSeededValues(t *testing.T) {
	t.Parallel()

	type seededConfig struct {
		Host  string   `strata:"host"`
		Port  int      `strata:"port"`
		Tags  []string `strata:"tags"`
		Debug bool     `strata:"debug"`
	}

	target := seededConfig{Host: "seeded.example", Port: 1111, Tags: []string{"a"}, Debug: true}

	path := writeFile(t, "sparse.toml", "port = 2222\n")
	if _, err := strata.LoadInto(&target, strata.WithPath(path), strata.WithFormats("toml")); err != nil {
		t.Fatalf("LoadInto: %v", err)
	}

	want := seededConfig{Host: "seeded.example", Port: 2222, Tags: []string{"a"}, Debug: true}
	if !reflect.DeepEqual(target, want) {
		t.Fatalf("target = %+v, want %+v", target, want)
	}
}

// Without options, only defaults apply: tier files under the program's own
// name and bare or prefixed env vars are all ignored.
func TestLoadWithoutOptionsReadsOnlyDefaults(t *testing.T) {
	systemBase, userBase := isolateTiers(t)
	program := filepath.Base(os.Args[0])

	for _, base := range []string{systemBase, userBase} {
		dir := filepath.Join(base, program)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("port = 9\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("PORT", "7")
	t.Setenv("MYAPP_PORT", "8")

	got, meta, err := strata.LoadWithMetadata[tierConfig]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if want := (tierConfig{Port: 1, Note: "default"}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	if files := meta.ActiveFiles(); len(files) != 0 {
		t.Fatalf("ActiveFiles = %v, want none", files)
	}
}

// A missing tier file is skipped, but a tier file that exists and cannot be
// read or parsed fails the load.
func TestBrokenTierFilesFailTheLoad(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, systemBase, userBase string)
		want  error
	}{
		{"system malformed", func(t *testing.T, systemBase, _ string) {
			t.Helper()
			writeTierFile(t, systemBase, "port = = 2\n")
		}, strata.ErrMalformed},
		{"user malformed", func(t *testing.T, _, userBase string) {
			t.Helper()
			writeTierFile(t, userBase, "port = = 3\n")
		}, strata.ErrMalformed},
		{"user unreadable", func(t *testing.T, _, userBase string) {
			t.Helper()

			if os.Geteuid() == 0 {
				t.Skip("root reads mode 000 files")
			}

			path := writeTierFile(t, userBase, "port = 3\n")
			if err := os.Chmod(path, 0o000); err != nil {
				t.Fatal(err)
			}
		}, fs.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			systemBase, userBase := isolateTiers(t)
			tc.setup(t, systemBase, userBase)

			got, err := strata.Load[tierConfig](strata.WithAppName("myapp"), strata.WithFormats("toml"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want it to wrap %v", err, tc.want)
			}

			if got != (tierConfig{}) {
				t.Fatalf("got %+v, want the zero value on error", got)
			}
		})
	}
}

// WithoutFiles ignores populated system and user tiers, but still reads the
// WithPath file.
func TestWithoutFilesSkipsPopulatedTiers(t *testing.T) {
	systemBase, userBase := isolateTiers(t)
	writeTierFile(t, systemBase, "port = 2\nnote = \"system\"\n")
	writeTierFile(t, userBase, "port = 3\nnote = \"user\"\n")
	path := writeFile(t, "explicit.toml", "port = 4\n")

	got, meta, err := strata.LoadWithMetadata[tierConfig](
		strata.WithAppName("myapp"), strata.WithFormats("toml"), strata.WithoutFiles(), strata.WithPath(path),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if want := (tierConfig{Port: 4, Note: "default"}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	if files := meta.ActiveFiles(); !reflect.DeepEqual(files, []string{path}) {
		t.Fatalf("ActiveFiles = %v, want [%s]", files, path)
	}
}

func TestLoadRejectsBadTargetsAndDirectoryPaths(t *testing.T) {
	t.Parallel()

	if _, err := strata.LoadInto[tierConfig](nil); !errors.Is(err, strata.ErrTargetNotPointer) {
		t.Errorf("nil target: err = %v, want ErrTargetNotPointer", err)
	}

	t.Run("non-struct target", func(t *testing.T) {
		t.Parallel()

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("LoadInto(*int) panicked: %v", r)
			}
		}()

		var notStruct int
		if _, err := strata.LoadInto(&notStruct); !errors.Is(err, strata.ErrTargetNotPointer) {
			t.Errorf("err = %v, want ErrTargetNotPointer", err)
		}
	})

	dir := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := strata.Load[tierConfig](strata.WithPath(dir), strata.WithFormats("toml")); !errors.Is(err, strata.ErrPathIsDirectory) {
		t.Errorf("directory path: err = %v, want ErrPathIsDirectory", err)
	}
}

// WithDefaults ranks below every file and env var: each layer above it wins
// for the keys it sets, and WithDefaults supplies the rest.
func TestWithDefaultsRanksBelowFilesAndEnv(t *testing.T) {
	t.Setenv("WD_HOST", "env.example")

	path := writeFile(t, "config.toml", "port = 9000\n")

	got, meta, err := strata.LoadWithMetadata[typoConfig](
		strata.WithDefaults(typoConfig{Host: "defaults.example", Port: 7000, Database: typoDatabase{Port: 5432}}),
		strata.WithPath(path), strata.WithFormats("toml"), strata.WithEnvPrefix("WD_"),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := typoConfig{Host: "env.example", Port: 9000, Database: typoDatabase{Port: 5432}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	for key, source := range map[string]strata.SourceKind{"host": strata.SourceEnv, portKey: strata.SourceFile, "database.port": strata.SourceDefault} {
		if origin, ok := meta.Where(key); !ok || origin.Source != source {
			t.Errorf("%s origin = %+v, want %s", key, origin, source)
		}
	}
}

// PanickyEmbedded is exported so that its SetDefaults, reached through a nil
// embedded pointer, is one the loader calls.
type PanickyEmbedded struct {
	Port int `strata:"port"`
}

func (p *PanickyEmbedded) SetDefaults() {
	p.Port = 1

	panic("embedded defaults exploded")
}

type nilEmbeddedDefaults struct {
	*PanickyEmbedded
}

// A panicking SetDefaults, including one promoted through a nil embedded
// pointer, fails the load with ErrSetDefaultsPanicked instead of crashing.
func TestPanickingDefaultsWrapTheSentinel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		load func() error
	}{
		{"top level", func() error { _, err := strata.Load[panickyConfig](); return err }},
		{"nested field", func() error { _, err := strata.Load[nestedPanicky](); return err }},
		{"nil embedded pointer", func() error { _, err := strata.Load[nilEmbeddedDefaults](); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Load panicked: %v", r)
				}
			}()

			if err := tc.load(); !errors.Is(err, strata.ErrSetDefaultsPanicked) {
				t.Fatalf("err = %v, want ErrSetDefaultsPanicked", err)
			}
		})
	}
}

// WithPath("-") reads stdin with the first listed format and records the
// stdin source on every key it sets.
func TestStdinOriginIsStdin(t *testing.T) {
	t.Parallel()

	got, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithPath("-"), strata.WithStdin(strings.NewReader("port: 9000\nhost: h\n")), strata.WithFormats("yaml", "toml"),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got != (formatsTestConfig{Port: 9000, Host: "h"}) {
		t.Fatalf("got %+v", got)
	}

	want := strata.Origin{Key: portKey, Source: strata.SourceStdin, Path: "-", Line: 1, RawValue: "9000"}
	if origin, _ := meta.Where(portKey); origin != want {
		t.Fatalf("port origin = %+v, want %+v", origin, want)
	}
}

// Input exactly at the limit loads; one byte more fails with ErrFileTooLarge,
// for a file and for stdin. A limit of zero means 1 MiB.
func TestMaxFileSizeBoundary(t *testing.T) {
	t.Parallel()

	const mib = 1 << 20

	// sized returns a valid TOML document of exactly n bytes.
	sized := func(n int) string {
		const head = "host = '"
		return head + strings.Repeat("a", n-len(head)-2) + "'\n"
	}

	for _, tc := range []struct {
		name  string
		limit int64
		size  int
		want  error
	}{
		{"at the limit", 64, 64, nil},
		{"one over the limit", 64, 65, strata.ErrFileTooLarge},
		{"zero means 1 MiB, at it", 0, mib, nil},
		{"zero means 1 MiB, one over", 0, mib + 1, strata.ErrFileTooLarge},
		{"negative means 1 MiB, one over", -1, mib + 1, strata.ErrFileTooLarge},
	} {
		doc := sized(tc.size)

		for source, opts := range map[string]func(t *testing.T) []strata.Option{
			"file": func(t *testing.T) []strata.Option {
				t.Helper()

				return []strata.Option{strata.WithPath(writeFile(t, "c.toml", doc))}
			},
			"stdin": func(*testing.T) []strata.Option {
				return []strata.Option{strata.WithPath("-"), strata.WithStdin(strings.NewReader(doc))}
			},
		} {
			t.Run(tc.name+" "+source, func(t *testing.T) {
				t.Parallel()

				got, err := strata.Load[formatsTestConfig](append(opts(t), strata.WithFormats("toml"), strata.WithMaxFileSize(tc.limit))...)
				if !errors.Is(err, tc.want) || tc.want == nil && err != nil {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}

				if tc.want == nil && len(got.Host) != tc.size-len("host = ''\n") {
					t.Fatalf("host has %d bytes, want the whole document read", len(got.Host))
				}
			})
		}
	}
}

type embeddedDefaults struct {
	Port int `strata:"port"`
}

func (e *embeddedDefaults) SetDefaults() { e.Port = 4242 }

type outerWithUnexportedEmbed struct {
	embeddedDefaults

	Host string `strata:"host"`
}

// SetDefaults runs on every nested struct that declares it, including one
// embedded through an unexported type.
func TestSetDefaultsRunsOnUnexportedEmbeddedStruct(t *testing.T) {
	t.Parallel()

	got, err := strata.Load[outerWithUnexportedEmbed]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Port != 4242 {
		t.Fatalf("port = %d, want 4242 from the embedded SetDefaults", got.Port)
	}
}

type outerWithNilUnexportedEmbed struct {
	*embeddedDefaults

	Host string `strata:"host"`
}

// A SetDefaults promoted through a nil unexported embedded pointer has no
// receiver the loader can allocate, so it is skipped rather than failing the
// load with a nil dereference.
func TestSetDefaultsSkipsNilUnexportedEmbeddedPointer(t *testing.T) {
	t.Parallel()

	got, err := strata.Load[outerWithNilUnexportedEmbed]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.embeddedDefaults != nil {
		t.Fatalf("embedded pointer = %+v, want it left nil", got.embeddedDefaults)
	}
}
