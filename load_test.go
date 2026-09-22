package strata_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"
)

type cascadingConfig struct {
	AppName     string        `toml:"app_name"     yaml:"app_name"`
	AutoClean   bool          `toml:"auto_clean"   yaml:"auto_clean"`
	MaxRetries  int           `toml:"max_retries"  yaml:"max_retries"`
	Timeout     time.Duration `toml:"timeout"      yaml:"timeout"`
	IgnorePaths []string      `toml:"ignore_paths" yaml:"ignore_paths"`
}

func (c *cascadingConfig) SetDefaults() {
	c.AppName = "default-app"
	c.AutoClean = true
	c.MaxRetries = 3
	c.Timeout = 10 * time.Second
	c.IgnorePaths = []string{"/tmp", "/var/tmp"}
}

func TestLoadInto(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "load_into.yaml")

	if err := os.WriteFile(filePath, []byte("app_name: into-app\nmax_retries: 42\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var target cascadingConfig

	meta, err := strata.LoadInto(&target, strata.WithExplicitPath(filePath))
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
	tmpDir := t.TempDir()
	projectFile := filepath.Join(tmpDir, ".precedence.toml")

	if err := os.WriteFile(projectFile, []byte("app_name = \"project-app\"\nmax_retries = 10\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("PREC_APP_NAME", "env-app")

	cfg, meta, err := strata.Load[cascadingConfig](
		strata.WithAppName("precedence"),
		strata.WithCWD(tmpDir),
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

	if orig, ok := meta.Where("max_retries"); !ok || orig.Source != strata.SourceProject {
		t.Errorf("max_retries origin = %+v, want SourceProject", orig)
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

	cfg, meta, err := strata.Load[cascadingConfig](
		strata.WithExplicitPath(filePath),
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

	if orig, ok := meta.Where("max_retries"); !ok || orig.Source != strata.SourceProject {
		t.Errorf("max_retries origin = %+v, want SourceProject", orig)
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

	cfg, _, err := strata.Load[cascadingConfig](
		strata.WithExplicitPath(filePath),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	wantPaths := []string{"/custom/build"}
	if !reflect.DeepEqual(cfg.IgnorePaths, wantPaths) {
		t.Fatalf("IgnorePaths = %v, want %v (Slice replacement failed!)", cfg.IgnorePaths, wantPaths)
	}
}

// An explicit path MUST outrank WithoutFiles. WithoutFiles once short-circuited
// first, loading nothing.
func TestExplicitPathOutranksWithoutFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cfg.toml")
	if err := os.WriteFile(path, []byte("small = 42\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, _, err := strata.Load[narrowConfig](strata.WithExplicitPath(path), strata.WithoutFiles())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Small != 42 {
		t.Fatalf("Small = %d, want 42", got.Small)
	}
}

type dummyCustomCodec struct {
	decoded bool
}

func (d *dummyCustomCodec) Decode(_ []byte, _ any) error {
	d.decoded = true
	return nil
}

func (d *dummyCustomCodec) Encode(_ any) ([]byte, error) {
	return []byte("dummy = true\n"), nil
}

func TestCustomCodecRegistration(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "config.custom")

	if err := os.WriteFile(customPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	customCodec := &dummyCustomCodec{decoded: false}

	type SimpleCfg struct {
		Value string `strata:"value"`
	}

	_, _, err := strata.Load[SimpleCfg](
		strata.WithExplicitPath(customPath),
		strata.WithCodec(".custom", customCodec),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if !customCodec.decoded {
		t.Errorf("customCodec.Decode was not called")
	}
}

func TestMaxFileSizeOption(t *testing.T) {
	t.Parallel()

	type Cfg struct {
		Data string `strata:"data"`
	}

	buf := bytes.NewBufferString(strings.Repeat("a", 200))

	_, _, err := strata.Load[Cfg](
		strata.WithExplicitPath("-"),
		strata.WithStdin(buf),
		strata.WithMaxFileSize(100),
	)
	if err == nil {
		t.Fatalf("expected error for exceeding max file size, got nil")
	}
}

// A size limit of [math.MaxInt64] MUST read the whole input. The limit-plus-one
// probe once overflowed to a negative value, so the read returned no bytes and
// the load succeeded with an empty configuration.
func TestMaxFileSizeAcceptsMaxInt64(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cfg.toml")
	if err := os.WriteFile(path, []byte("small = 7\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, _, err := strata.Load[narrowConfig](strata.WithExplicitPath(path), strata.WithMaxFileSize(math.MaxInt64))
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

	_, _, err := strata.Load[narrowConfig](strata.WithExplicitPath(path), strata.WithMaxFileSize(4))
	if !errors.Is(err, strata.ErrFileTooLarge) {
		t.Fatalf("err = %v, want it to wrap ErrFileTooLarge", err)
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

// A panic from SetDefaults MUST be reported. It was once swallowed, so a load
// whose defaults were never applied returned successfully.
func TestPanickingDefaultsIsReported(t *testing.T) {
	t.Parallel()

	_, _, err := strata.Load[panickyConfig](strata.WithoutFiles())
	if err == nil || !strings.Contains(err.Error(), "defaults exploded") {
		t.Fatalf("top-level err = %v, want the panic reported", err)
	}

	if _, _, err := strata.Load[nestedPanicky](strata.WithoutFiles()); err == nil {
		t.Fatal("nested panicking SetDefaults returned no error")
	} else if !strings.Contains(err.Error(), "defaults exploded") {
		t.Errorf("err = %v, want it to carry the panicking value", err)
	}
}

type formatsTestConfig struct {
	Port int    `json:"port" strata:"port" toml:"port" yaml:"port"`
	Host string `json:"host" strata:"host" toml:"host" yaml:"host"`
}

func TestWithFormatsRestrictsDiscovery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tomlPath := filepath.Join(dir, ".myapp.toml")

	if err := os.WriteFile(tomlPath, []byte("port = 8080\nhost = 'toml-host'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	yamlPath := filepath.Join(dir, ".myapp.yaml")

	if err := os.WriteFile(yamlPath, []byte("port: 9090\nhost: yaml-host\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	defaultCfg, defaultMeta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
	)
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}

	if defaultCfg.Port != 8080 {
		t.Fatalf("defaultCfg.Port = %d, want 8080", defaultCfg.Port)
	}

	if defaultMeta.ActiveFiles()[0] != tomlPath {
		t.Fatalf("active file = %s, want %s", defaultMeta.ActiveFiles()[0], tomlPath)
	}

	yamlCfg, yamlMeta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormats("yaml"),
	)
	if err != nil {
		t.Fatalf("Load WithFormats: %v", err)
	}

	if yamlCfg.Port != 9090 {
		t.Fatalf("yamlCfg.Port = %d, want 9090", yamlCfg.Port)
	}

	if yamlMeta.ActiveFiles()[0] != yamlPath {
		t.Fatalf("active file = %s, want %s", yamlMeta.ActiveFiles()[0], yamlPath)
	}
}

func TestWithFormatsReordersPriority(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tomlPath := filepath.Join(dir, ".myapp.toml")

	if err := os.WriteFile(tomlPath, []byte("port = 8080\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	jsonPath := filepath.Join(dir, ".myapp.json")

	if err := os.WriteFile(jsonPath, []byte(`{"port": 7070}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormats("json", "toml"),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != 7070 {
		t.Fatalf("cfg.Port = %d, want 7070", cfg.Port)
	}

	if meta.ActiveFiles()[0] != jsonPath {
		t.Fatalf("active file = %s, want %s", meta.ActiveFiles()[0], jsonPath)
	}
}

func TestWithFormatsYAMLExpansion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ymlPath := filepath.Join(dir, ".myapp.yml")

	if err := os.WriteFile(ymlPath, []byte("port: 9090\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, _, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormats("yaml"),
	)
	if err != nil {
		t.Fatalf("Load with format name yaml: %v", err)
	}

	if cfg.Port != 9090 {
		t.Fatalf("cfg.Port = %d, want 9090", cfg.Port)
	}

	cfgDot, _, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormats(".yaml"),
	)
	if err != nil {
		t.Fatalf("Load with explicit .yaml: %v", err)
	}

	if cfgDot.Port != 0 {
		t.Fatalf("cfgDot.Port = %d, want 0 (defaults, .yml ignored)", cfgDot.Port)
	}
}

func TestWithFormatsUnknownFormatError(t *testing.T) {
	t.Parallel()

	_, _, err := strata.Load[formatsTestConfig](
		strata.WithFormats("tmol"),
	)
	if err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}

	if !errors.Is(err, strata.ErrNoCodec) {
		t.Fatalf("err = %v, want it to wrap ErrNoCodec", err)
	}

	if !strings.Contains(err.Error(), "tmol") {
		t.Fatalf("err = %v, want it to mention 'tmol'", err)
	}
}

func TestWithFormatsCustomCodec(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	customPath := filepath.Join(dir, "config.custom")

	if err := os.WriteFile(customPath, []byte("custom-content"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c1 := &dummyCustomCodec{}

	_, _, err := strata.Load[formatsTestConfig](
		strata.WithExplicitPath(customPath),
		strata.WithCodec(".custom", c1),
		strata.WithFormats(".custom"),
	)
	if err != nil {
		t.Fatalf("Load WithCodec then WithFormats: %v", err)
	}

	if !c1.decoded {
		t.Fatal("codec 1 was not called")
	}

	c2 := &dummyCustomCodec{}

	_, _, err = strata.Load[formatsTestConfig](
		strata.WithExplicitPath(customPath),
		strata.WithFormats(".custom"),
		strata.WithCodec(".custom", c2),
	)
	if err != nil {
		t.Fatalf("Load WithFormats then WithCodec: %v", err)
	}

	if !c2.decoded {
		t.Fatal("codec 2 was not called")
	}
}

func TestWithFormatsExplicitPathDisallowed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(yamlPath, []byte("port: 9090\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, _, err := strata.Load[formatsTestConfig](
		strata.WithExplicitPath(yamlPath),
		strata.WithFormats(".toml"),
	)
	if err == nil {
		t.Fatal("expected error for explicit path with disallowed format, got nil")
	}

	if !errors.Is(err, strata.ErrNoCodec) {
		t.Fatalf("err = %v, want it to wrap ErrNoCodec", err)
	}
}

func TestWithFormatAliasTOML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, ".myapp.conf")

	if err := os.WriteFile(confPath, []byte("port = 8080\nhost = 'conf-host'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormatAlias(".conf", "toml"),
	)
	if err != nil {
		t.Fatalf("Load WithFormatAlias: %v", err)
	}

	if cfg.Port != 8080 || cfg.Host != "conf-host" {
		t.Fatalf("cfg = %+v, want port 8080, host conf-host", cfg)
	}

	if len(meta.ActiveFiles()) != 1 || meta.ActiveFiles()[0] != confPath {
		t.Fatalf("ActiveFiles = %v, want [%s]", meta.ActiveFiles(), confPath)
	}

	origin, ok := meta.Where("port")
	if !ok {
		t.Fatal("Where(\"port\") returned false, want true")
	}

	if origin.Source != "project" || origin.Path != confPath {
		t.Fatalf("origin = %+v, want project and %s", origin, confPath)
	}
}

func TestWithFormatAliasYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, ".myapp.conf")

	if err := os.WriteFile(confPath, []byte("port: 9090\nhost: yaml-conf-host\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormatAlias(".conf", "yaml"),
	)
	if err != nil {
		t.Fatalf("Load WithFormatAlias yaml: %v", err)
	}

	if cfg.Port != 9090 || cfg.Host != "yaml-conf-host" {
		t.Fatalf("cfg = %+v, want port 9090, host yaml-conf-host", cfg)
	}

	if len(meta.ActiveFiles()) != 1 || meta.ActiveFiles()[0] != confPath {
		t.Fatalf("ActiveFiles = %v, want [%s]", meta.ActiveFiles(), confPath)
	}
}

func TestWithFormatAliasUnknownTarget(t *testing.T) {
	t.Parallel()

	_, _, err := strata.Load[formatsTestConfig](
		strata.WithFormatAlias(".conf", "nonexistent"),
	)
	if err == nil {
		t.Fatal("expected error for unknown alias target, got nil")
	}

	if !errors.Is(err, strata.ErrNoCodec) {
		t.Fatalf("err = %v, want it to wrap ErrNoCodec", err)
	}
}

func TestWithDecoder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	customPath := filepath.Join(dir, ".myapp.custom")

	if err := os.WriteFile(customPath, []byte(`{"port": 5050, "host": "decoder-host"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithDecoder(".custom", json.Unmarshal),
	)
	if err != nil {
		t.Fatalf("Load WithDecoder: %v", err)
	}

	if cfg.Port != 5050 || cfg.Host != "decoder-host" {
		t.Fatalf("cfg = %+v, want port 5050 and host decoder-host", cfg)
	}

	if len(meta.ActiveFiles()) != 1 || meta.ActiveFiles()[0] != customPath {
		t.Fatalf("ActiveFiles = %v, want [%s]", meta.ActiveFiles(), customPath)
	}
}

func TestWithDecoderFunc(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iniPath := filepath.Join(dir, ".myapp.ini")

	if err := os.WriteFile(iniPath, []byte("port=4040\nhost=ini-host\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithDecoderFunc(".ini", func(data []byte, target *formatsTestConfig) error {
			for line := range strings.SplitSeq(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				parts := strings.SplitN(line, "=", 2)
				if len(parts) != 2 {
					continue
				}

				switch parts[0] {
				case "port":
					p, pErr := strconv.Atoi(parts[1])
					if pErr != nil {
						return fmt.Errorf("parse port: %w", pErr)
					}

					target.Port = p
				case "host":
					target.Host = parts[1]
				}
			}

			return nil
		}),
	)
	if err != nil {
		t.Fatalf("Load WithDecoderFunc: %v", err)
	}

	if cfg.Port != 4040 || cfg.Host != "ini-host" {
		t.Fatalf("cfg = %+v, want port 4040 and host ini-host", cfg)
	}

	if len(meta.ActiveFiles()) != 1 || meta.ActiveFiles()[0] != iniPath {
		t.Fatalf("ActiveFiles = %v, want [%s]", meta.ActiveFiles(), iniPath)
	}
}

func TestWithDecoderNilPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected WithDecoder(nil) to panic")
		}
	}()

	strata.WithDecoder(".custom", nil)
}

func TestWithDecoderFuncNilPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected WithDecoderFunc(nil) to panic")
		}
	}()

	strata.WithDecoderFunc[formatsTestConfig](".custom", nil)
}

func TestWithDecoderMalformed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	customPath := filepath.Join(dir, ".myapp.custom")

	if err := os.WriteFile(customPath, []byte("not valid json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, _, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithDecoder(".custom", json.Unmarshal),
	)
	if err == nil {
		t.Fatal("expected error for malformed decoder data, got nil")
	}

	if !errors.Is(err, strata.ErrMalformed) {
		t.Fatalf("err = %v, want it to wrap ErrMalformed", err)
	}
}

type defaultedFormatsConfig struct {
	Port int    `json:"port" strata:"port" toml:"port" yaml:"port"`
	Host string `json:"host" strata:"host" toml:"host" yaml:"host"`
}

func (d *defaultedFormatsConfig) SetDefaults() {
	d.Port = 1234
	d.Host = "default-host"
}

func TestWithFormatAliasTransitive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".myapp.cfg")

	if err := os.WriteFile(cfgPath, []byte("port = 8181\nhost = 'transitive-host'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormatAlias(".cfg", ".conf"),
		strata.WithFormatAlias(".conf", "toml"),
	)
	if err != nil {
		t.Fatalf("Load WithFormatAlias transitive: %v", err)
	}

	if cfg.Port != 8181 || cfg.Host != "transitive-host" {
		t.Fatalf("cfg = %+v, want port 8181 and host transitive-host", cfg)
	}

	if len(meta.ActiveFiles()) != 1 || meta.ActiveFiles()[0] != cfgPath {
		t.Fatalf("ActiveFiles = %v, want [%s]", meta.ActiveFiles(), cfgPath)
	}

	origin, ok := meta.Where("port")
	if !ok {
		t.Fatal("Where(\"port\") returned false, want true")
	}

	if origin.Source != "project" || origin.Path != cfgPath {
		t.Fatalf("origin = %+v, want project and %s", origin, cfgPath)
	}
}

func TestWithFormatAliasJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, ".myapp.manifest")

	if err := os.WriteFile(manifestPath, []byte(`{"port": 7272, "host": "manifest-host"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormatAlias(".manifest", "json"),
	)
	if err != nil {
		t.Fatalf("Load WithFormatAlias json: %v", err)
	}

	if cfg.Port != 7272 || cfg.Host != "manifest-host" {
		t.Fatalf("cfg = %+v, want port 7272 and host manifest-host", cfg)
	}

	origin, ok := meta.Where("host")
	if !ok {
		t.Fatal("Where(\"host\") returned false, want true")
	}

	if origin.Source != "project" || origin.Path != manifestPath {
		t.Fatalf("origin = %+v, want project and %s", origin, manifestPath)
	}
}

func TestWithFormatAliasNormalization(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, ".myapp.conf")

	if err := os.WriteFile(confPath, []byte("port = 8282\nhost = 'norm-host'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, _, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormatAlias("CONF", "TOML"),
		strata.WithFormatAlias("", "toml"),
		strata.WithFormatAlias(".empty", ""),
	)
	if err != nil {
		t.Fatalf("Load WithFormatAlias normalized: %v", err)
	}

	if cfg.Port != 8282 || cfg.Host != "norm-host" {
		t.Fatalf("cfg = %+v, want port 8282 and host norm-host", cfg)
	}
}

func TestWithFormatAliasSparseOverlay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	confPath := filepath.Join(dir, ".myapp.conf")

	if err := os.WriteFile(confPath, []byte("port = 5678\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, _, err := strata.Load[defaultedFormatsConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormatAlias(".conf", "toml"),
	)
	if err != nil {
		t.Fatalf("Load WithFormatAlias sparse: %v", err)
	}

	if cfg.Port != 5678 {
		t.Fatalf("cfg.Port = %d, want 5678 (overridden)", cfg.Port)
	}

	if cfg.Host != "default-host" {
		t.Fatalf("cfg.Host = %q, want default-host (preserved from defaults)", cfg.Host)
	}
}

func TestWithFormatAliasCombinedWithFormats(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tomlPath := filepath.Join(dir, ".myapp.toml")
	confPath := filepath.Join(dir, ".myapp.conf")

	if err := os.WriteFile(tomlPath, []byte("port = 1000\nhost = 'toml'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile toml: %v", err)
	}

	if err := os.WriteFile(confPath, []byte("port = 2000\nhost = 'conf'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile conf: %v", err)
	}

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormatAlias(".conf", "toml"),
		strata.WithFormats(".conf"),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != 2000 || cfg.Host != "conf" {
		t.Fatalf("cfg = %+v, want port 2000 and host conf", cfg)
	}

	if len(meta.ActiveFiles()) != 1 || meta.ActiveFiles()[0] != confPath {
		t.Fatalf("ActiveFiles = %v, want [%s]", meta.ActiveFiles(), confPath)
	}
}

func TestWithFormatAliasCascadingTiers(t *testing.T) {
	dir := t.TempDir()
	userDir := filepath.Join(dir, "user")
	appUserDir := filepath.Join(userDir, "myapp")
	projDir := filepath.Join(dir, "proj")

	if err := os.MkdirAll(appUserDir, 0o700); err != nil {
		t.Fatalf("MkdirAll user: %v", err)
	}

	if err := os.MkdirAll(projDir, 0o700); err != nil {
		t.Fatalf("MkdirAll proj: %v", err)
	}

	userConf := filepath.Join(appUserDir, "config.conf")
	projConf := filepath.Join(projDir, ".myapp.conf")

	if err := os.WriteFile(userConf, []byte("port = 2000\nhost = 'user-host'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user: %v", err)
	}

	if err := os.WriteFile(projConf, []byte("port = 3000\n"), 0o600); err != nil {
		t.Fatalf("WriteFile proj: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", userDir)

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(projDir),
		strata.WithFormatAlias(".conf", "toml"),
	)
	if err != nil {
		t.Fatalf("Load cascading alias: %v", err)
	}

	if cfg.Port != 3000 {
		t.Fatalf("cfg.Port = %d, want 3000 (project tier override)", cfg.Port)
	}

	if cfg.Host != "user-host" {
		t.Fatalf("cfg.Host = %q, want user-host (user tier preserved)", cfg.Host)
	}

	if len(meta.ActiveFiles()) != 2 {
		t.Fatalf("ActiveFiles = %v, want 2 files", meta.ActiveFiles())
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

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(t.TempDir()),
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

func TestWithDecoderCodecContracts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	customPath := filepath.Join(dir, ".myapp.custom")

	if err := os.WriteFile(customPath, []byte(`{"port": 5555}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, _, err := strata.Load[defaultedFormatsConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithDecoder(".custom", json.Unmarshal),
	)
	if err != nil {
		t.Fatalf("Load WithDecoder: %v", err)
	}

	if cfg.Port != 5555 {
		t.Fatalf("cfg.Port = %d, want 5555", cfg.Port)
	}

	if cfg.Host != "default-host" {
		t.Fatalf("cfg.Host = %q, want default-host (preserved from defaults)", cfg.Host)
	}
}

func TestWithDecoderFuncTypeMismatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")

	if err := os.WriteFile(iniPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var mismatched cascadingConfig

	_, err := strata.LoadInto(&mismatched,
		strata.WithExplicitPath(iniPath),
		strata.WithDecoderFunc(".ini", func(_ []byte, _ *formatsTestConfig) error {
			return nil
		}),
	)
	if err == nil {
		t.Fatal("expected type mismatch error, got nil")
	}

	if !errors.Is(err, strata.ErrCodecTargetMismatch) {
		t.Fatalf("err = %v, want it to wrap ErrCodecTargetMismatch", err)
	}
}

func TestWithDecoderFuncNilTargetAndErrorWrapping(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")

	if err := os.WriteFile(iniPath, []byte("bad-data"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	customErr := errors.New("custom parse failure")

	_, _, err := strata.Load[formatsTestConfig](
		strata.WithExplicitPath(iniPath),
		strata.WithDecoderFunc(".ini", func(_ []byte, _ *formatsTestConfig) error {
			return customErr
		}),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, strata.ErrMalformed) {
		t.Fatalf("err = %v, want it to wrap ErrMalformed", err)
	}

	if !errors.Is(err, customErr) {
		t.Fatalf("err = %v, want it to wrap customErr", err)
	}
}

func TestWithFormatsEmptyArgDisablesDiscovery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tomlPath := filepath.Join(dir, ".myapp.toml")

	if err := os.WriteFile(tomlPath, []byte("port = 8080\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, meta, err := strata.Load[defaultedFormatsConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormats(),
	)
	if err != nil {
		t.Fatalf("Load WithFormats(): %v", err)
	}

	if len(meta.ActiveFiles()) != 0 {
		t.Fatalf("ActiveFiles = %v, want 0 files", meta.ActiveFiles())
	}

	if cfg.Port != 1234 {
		t.Fatalf("cfg.Port = %d, want default 1234", cfg.Port)
	}
}

func TestWithFormatsWhitespaceAndCase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tomlPath := filepath.Join(dir, ".myapp.toml")

	if err := os.WriteFile(tomlPath, []byte("port = 8080\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, meta, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithCWD(dir),
		strata.WithFormats("  ", "TOML"),
	)
	if err != nil {
		t.Fatalf("Load WithFormats whitespace/case: %v", err)
	}

	if cfg.Port != 8080 {
		t.Fatalf("cfg.Port = %d, want 8080", cfg.Port)
	}

	if len(meta.ActiveFiles()) != 1 {
		t.Fatalf("ActiveFiles = %v, want 1 file", meta.ActiveFiles())
	}
}

func TestTableFirstTOMLStdinDetection(t *testing.T) {
	t.Parallel()

	type serverCfg struct {
		Server struct {
			Port int `strata:"port"`
		} `strata:"server"`
	}

	buf := strings.NewReader("[server]\nport = 9000\n")

	cfg, _, err := strata.Load[serverCfg](
		strata.WithExplicitPath("-"),
		strata.WithStdin(buf),
	)
	if err != nil {
		t.Fatalf("Load stdin error: %v", err)
	}

	if cfg.Server.Port != 9000 {
		t.Errorf("Server.Port = %d, want 9000", cfg.Server.Port)
	}
}

func TestYMLOnlyStdinDetection(t *testing.T) {
	t.Parallel()

	type simpleCfg struct {
		Port int `strata:"port"`
	}

	buf := strings.NewReader("port: 9000\n")

	cfg, _, err := strata.Load[simpleCfg](
		strata.WithExplicitPath("-"),
		strata.WithStdin(buf),
		strata.WithFormats(".yml"),
	)
	if err != nil {
		t.Fatalf("Load .yml-only stdin error: %v", err)
	}

	if cfg.Port != 9000 {
		t.Errorf("Port = %d, want 9000", cfg.Port)
	}
}

func TestWithFormatsYMLPathExcludedWhenYAMLOnly(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	ymlPath := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(ymlPath, []byte("port: 9000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	type simpleCfg struct {
		Port int `strata:"port"`
	}

	_, _, err := strata.Load[simpleCfg](
		strata.WithExplicitPath(ymlPath),
		strata.WithFormats(".yaml"),
	)
	if err == nil {
		t.Fatal("expected error for explicit .yml path with only .yaml enabled, got nil")
	}

	if !errors.Is(err, strata.ErrNoCodec) {
		t.Errorf("expected ErrNoCodec, got %v", err)
	}
}
