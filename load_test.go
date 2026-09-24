package strata_test

import (
	"bytes"
	"errors"
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

	if orig, ok := meta.Where("port"); !ok || orig.Source != strata.SourceUser {
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

	origin, ok := meta.Where("port")
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

	if origin, ok := meta.Where("port"); !ok || origin.Source != strata.SourceDefault || origin.RawValue != "7000" {
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
