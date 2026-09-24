package strata_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zigai/strata"
)

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

type defaultedFormatsConfig struct {
	Port int    `json:"port" strata:"port" toml:"port" yaml:"port"`
	Host string `json:"host" strata:"host" toml:"host" yaml:"host"`
}

func (d *defaultedFormatsConfig) SetDefaults() {
	d.Port = 1234
	d.Host = "default-host"
}

func TestCustomCodecRegistration(t *testing.T) {
	t.Parallel()

	customPath := writeFile(t, "config.custom", "data")

	customCodec := &dummyCustomCodec{decoded: false}

	type SimpleCfg struct {
		Value string `strata:"value"`
	}

	_, err := strata.Load[SimpleCfg](
		strata.WithPath(customPath),
		strata.WithCodec(".custom", customCodec),
		strata.WithFormats(".custom"),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if !customCodec.decoded {
		t.Errorf("customCodec.Decode was not called")
	}
}

func TestWithFormatsRestrictsDiscovery(t *testing.T) {
	dir := t.TempDir()

	appDir := filepath.Join(dir, "myapp")
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	tomlPath := filepath.Join(appDir, "config.toml")
	if err := os.WriteFile(tomlPath, []byte("port = 8080\nhost = 'toml-host'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	yamlPath := filepath.Join(appDir, "config.yaml")
	if err := os.WriteFile(yamlPath, []byte("port: 9090\nhost: yaml-host\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)

	if _, err := strata.Load[formatsTestConfig](strata.WithAppName("myapp")); !errors.Is(err, strata.ErrNoFormats) {
		t.Fatalf("Load without formats error = %v, want ErrNoFormats", err)
	}

	tomlCfg, tomlMeta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithFormats("toml"),
	)
	if err != nil || tomlCfg.Port != 8080 || tomlMeta.ActiveFiles()[0] != tomlPath {
		t.Fatalf("TOML load = %+v, %v, %v", tomlCfg, tomlMeta, err)
	}

	yamlCfg, yamlMeta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithAppName("myapp"),
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

func TestDiscoveryRequiresFormatsWithoutExistingFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())

	for _, options := range [][]strata.Option{
		{strata.WithAppName("myapp")},
		{strata.WithAppName("myapp"), strata.WithPath(writeFile(t, "config.toml", "port = 8080\n"))},
	} {
		if _, err := strata.Load[formatsTestConfig](options...); !errors.Is(err, strata.ErrNoFormats) {
			t.Fatalf("Load without discovery formats error = %v, want ErrNoFormats", err)
		}
	}

	if _, err := strata.Load[defaultedFormatsConfig](); err != nil {
		t.Fatalf("defaults-only load: %v", err)
	}

	if _, err := strata.Load[defaultedFormatsConfig](strata.WithAppName("myapp"), strata.WithoutFiles()); err != nil {
		t.Fatalf("disabled file discovery: %v", err)
	}
}

func TestNamedPathUsesExtension(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"config", "config.conf"} {
		path := writeFile(t, name, "port = 8080\n")
		if _, err := strata.Load[formatsTestConfig](strata.WithPath(path)); !errors.Is(err, strata.ErrNoFormats) {
			t.Fatalf("Load(%q) error = %v, want ErrNoFormats", path, err)
		}

		if _, err := strata.Load[formatsTestConfig](strata.WithPath(path), strata.WithFormats("toml")); !errors.Is(err, strata.ErrUnsupportedFormat) {
			t.Fatalf("Load(%q) with TOML enabled error = %v, want ErrUnsupportedFormat", path, err)
		}
	}

	jsonPath := writeFile(t, "config.json", "port = 8080\n")
	if _, err := strata.Load[formatsTestConfig](strata.WithPath(jsonPath), strata.WithFormats("json")); !errors.Is(err, strata.ErrMalformed) {
		t.Fatalf("Load JSON with TOML content error = %v, want ErrMalformed", err)
	}

	missingYAML := filepath.Join(t.TempDir(), "missing.yaml")
	if _, err := strata.Load[formatsTestConfig](strata.WithOptionalPath(missingYAML)); !errors.Is(err, strata.ErrNoFormats) {
		t.Fatalf("Load optional path without formats error = %v, want ErrNoFormats", err)
	}

	if _, err := strata.Load[formatsTestConfig](strata.WithOptionalPath(missingYAML), strata.WithFormats("toml")); !errors.Is(err, strata.ErrUnsupportedFormat) {
		t.Fatalf("Load missing disallowed YAML error = %v, want ErrUnsupportedFormat", err)
	}

	aliasPath := writeFile(t, "config.conf", "port = 8080\n")
	if _, err := strata.Load[formatsTestConfig](strata.WithPath(aliasPath), strata.WithFormatAlias(".conf", "toml"), strata.WithFormats("toml")); !errors.Is(err, strata.ErrUnsupportedFormat) {
		t.Fatalf("Load unlisted alias error = %v, want ErrUnsupportedFormat", err)
	}

	customPath := writeFile(t, "config.custom", "data")
	if _, err := strata.Load[formatsTestConfig](strata.WithPath(customPath), strata.WithCodec(".custom", &dummyCustomCodec{}), strata.WithFormats("toml")); !errors.Is(err, strata.ErrUnsupportedFormat) {
		t.Fatalf("Load unlisted codec error = %v, want ErrUnsupportedFormat", err)
	}
}

func TestStdinUsesFirstFormat(t *testing.T) {
	t.Parallel()

	for _, options := range [][]strata.Option{
		nil,
		{strata.WithFormats()},
	} {
		selected := append([]strata.Option{strata.WithPath("-"), strata.WithStdin(strings.NewReader("port = 8080\n"))}, options...)
		if _, err := strata.Load[formatsTestConfig](selected...); !errors.Is(err, strata.ErrNoFormats) {
			t.Fatalf("Load stdin with %d format options error = %v, want ErrNoFormats", len(options), err)
		}
	}

	cfg, err := strata.Load[formatsTestConfig](strata.WithPath("-"), strata.WithStdin(strings.NewReader(`{"port": 8080}`)), strata.WithFormats("json", "toml"))
	if err != nil || cfg.Port != 8080 {
		t.Fatalf("Load JSON stdin = %+v, %v", cfg, err)
	}
}

func TestWithFormatsReordersPriority(t *testing.T) {
	dir := t.TempDir()

	appDir := filepath.Join(dir, "myapp")
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	tomlPath := filepath.Join(appDir, "config.toml")
	if err := os.WriteFile(tomlPath, []byte("port = 8080\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	jsonPath := filepath.Join(appDir, "config.json")
	if err := os.WriteFile(jsonPath, []byte(`{"port": 7070}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithAppName("myapp"),
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
	dir := t.TempDir()

	appDir := filepath.Join(dir, "myapp")
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	ymlPath := filepath.Join(appDir, "config.yml")
	if err := os.WriteFile(ymlPath, []byte("port: 9090\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithFormats("yaml"),
	)
	if err != nil {
		t.Fatalf("Load with format name yaml: %v", err)
	}

	if cfg.Port != 9090 {
		t.Fatalf("cfg.Port = %d, want 9090", cfg.Port)
	}

	cfgDot, err := strata.Load[formatsTestConfig](
		strata.WithAppName("myapp"),
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

	_, err := strata.Load[formatsTestConfig](
		strata.WithFormats("tmol"),
	)
	if err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}

	if !errors.Is(err, strata.ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want it to wrap ErrUnsupportedFormat", err)
	}

	if !strings.Contains(err.Error(), "tmol") {
		t.Fatalf("err = %v, want it to mention 'tmol'", err)
	}
}

func TestWithFormatsCustomCodec(t *testing.T) {
	t.Parallel()

	customPath := writeFile(t, "config.custom", "custom-content")

	c1 := &dummyCustomCodec{}

	_, err := strata.Load[formatsTestConfig](
		strata.WithPath(customPath),
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

	_, err = strata.Load[formatsTestConfig](
		strata.WithPath(customPath),
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

	yamlPath := writeFile(t, "config.yaml", "port: 9090\n")

	_, err := strata.Load[formatsTestConfig](
		strata.WithPath(yamlPath),
		strata.WithFormats(".toml"),
	)
	if err == nil {
		t.Fatal("expected error for explicit path with disallowed format, got nil")
	}

	if !errors.Is(err, strata.ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want it to wrap ErrUnsupportedFormat", err)
	}
}

func TestWithFormatAliasTOML(t *testing.T) {
	t.Parallel()

	confPath := writeFile(t, "config.conf", "port = 8080\nhost = 'conf-host'\n")

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithPath(confPath),
		strata.WithFormatAlias(".conf", "toml"),
		strata.WithFormats(".conf"),
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

	if origin.Source != "file" || origin.Path != confPath {
		t.Fatalf("origin = %+v, want file and %s", origin, confPath)
	}
}

func TestWithFormatAliasYAML(t *testing.T) {
	t.Parallel()

	confPath := writeFile(t, "config.conf", "port: 9090\nhost: yaml-conf-host\n")

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithPath(confPath),
		strata.WithFormatAlias(".conf", "yaml"),
		strata.WithFormats(".conf"),
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

	_, err := strata.Load[formatsTestConfig](
		strata.WithFormatAlias(".conf", "nonexistent"),
	)
	if err == nil {
		t.Fatal("expected error for unknown alias target, got nil")
	}

	if !errors.Is(err, strata.ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want it to wrap ErrUnsupportedFormat", err)
	}
}

func TestWithDecoder(t *testing.T) {
	t.Parallel()

	customPath := writeFile(t, "config.custom", `{"port": 5050, "host": "decoder-host"}`)

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithPath(customPath),
		strata.WithDecoder(".custom", json.Unmarshal),
		strata.WithFormats(".custom"),
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

	iniPath := writeFile(t, "config.ini", "port=4040\nhost=ini-host\n")

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithPath(iniPath),
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
		strata.WithFormats(".ini"),
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

	customPath := writeFile(t, "config.custom", "not valid json")

	_, err := strata.Load[formatsTestConfig](
		strata.WithPath(customPath),
		strata.WithDecoder(".custom", json.Unmarshal),
		strata.WithFormats(".custom"),
	)
	if err == nil {
		t.Fatal("expected error for malformed decoder data, got nil")
	}

	if !errors.Is(err, strata.ErrMalformed) {
		t.Fatalf("err = %v, want it to wrap ErrMalformed", err)
	}
}

func TestWithFormatAliasTransitive(t *testing.T) {
	t.Parallel()

	cfgPath := writeFile(t, "config.cfg", "port = 8181\nhost = 'transitive-host'\n")

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithPath(cfgPath),
		strata.WithFormatAlias(".cfg", ".conf"),
		strata.WithFormatAlias(".conf", "toml"),
		strata.WithFormats(".cfg"),
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

	if origin.Source != "file" || origin.Path != cfgPath {
		t.Fatalf("origin = %+v, want file and %s", origin, cfgPath)
	}
}

func TestWithFormatAliasJSON(t *testing.T) {
	t.Parallel()

	manifestPath := writeFile(t, "config.manifest", `{"port": 7272, "host": "manifest-host"}`)

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithPath(manifestPath),
		strata.WithFormatAlias(".manifest", "json"),
		strata.WithFormats(".manifest"),
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

	if origin.Source != "file" || origin.Path != manifestPath {
		t.Fatalf("origin = %+v, want file and %s", origin, manifestPath)
	}
}

func TestWithFormatAliasNormalization(t *testing.T) {
	t.Parallel()

	confPath := writeFile(t, "config.conf", "port = 8282\nhost = 'norm-host'\n")

	cfg, err := strata.Load[formatsTestConfig](
		strata.WithPath(confPath),
		strata.WithFormatAlias("CONF", "TOML"),
		strata.WithFormatAlias("", "toml"),
		strata.WithFormatAlias(".empty", ""),
		strata.WithFormats(".conf"),
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

	confPath := writeFile(t, "config.conf", "port = 5678\n")

	cfg, err := strata.Load[defaultedFormatsConfig](
		strata.WithPath(confPath),
		strata.WithFormatAlias(".conf", "toml"),
		strata.WithFormats(".conf"),
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
	dir := t.TempDir()

	appDir := filepath.Join(dir, "myapp")
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	tomlPath := filepath.Join(appDir, "config.toml")
	confPath := filepath.Join(appDir, "config.conf")

	if err := os.WriteFile(tomlPath, []byte("port = 1000\nhost = 'toml'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile toml: %v", err)
	}

	if err := os.WriteFile(confPath, []byte("port = 2000\nhost = 'conf'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile conf: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithAppName("myapp"),
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
	projConf := filepath.Join(projDir, "config.conf")

	if err := os.WriteFile(userConf, []byte("port = 2000\nhost = 'user-host'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user: %v", err)
	}

	if err := os.WriteFile(projConf, []byte("port = 3000\n"), 0o600); err != nil {
		t.Fatalf("WriteFile proj: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", userDir)

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithAppName("myapp"),
		strata.WithPath(projConf),
		strata.WithFormatAlias(".conf", "toml"),
		strata.WithFormats(".conf"),
	)
	if err != nil {
		t.Fatalf("Load cascading alias: %v", err)
	}

	if cfg.Port != 3000 {
		t.Fatalf("cfg.Port = %d, want 3000 (config file override)", cfg.Port)
	}

	if cfg.Host != "user-host" {
		t.Fatalf("cfg.Host = %q, want user-host (user tier preserved)", cfg.Host)
	}

	if len(meta.ActiveFiles()) != 2 {
		t.Fatalf("ActiveFiles = %v, want 2 files", meta.ActiveFiles())
	}
}

func TestWithDecoderCodecContracts(t *testing.T) {
	t.Parallel()

	customPath := writeFile(t, "config.custom", `{"port": 5555}`)

	cfg, err := strata.Load[defaultedFormatsConfig](
		strata.WithPath(customPath),
		strata.WithDecoder(".custom", json.Unmarshal),
		strata.WithFormats(".custom"),
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

	iniPath := writeFile(t, "config.ini", "data")

	var mismatched cascadingConfig

	_, err := strata.LoadInto(
		&mismatched,
		strata.WithPath(iniPath),
		strata.WithDecoderFunc(".ini", func(_ []byte, _ *formatsTestConfig) error {
			return nil
		}),
		strata.WithFormats(".ini"),
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

	iniPath := writeFile(t, "config.ini", "bad-data")

	customErr := errors.New("custom parse failure")

	_, err := strata.Load[formatsTestConfig](
		strata.WithPath(iniPath),
		strata.WithDecoderFunc(".ini", func(_ []byte, _ *formatsTestConfig) error {
			return customErr
		}),
		strata.WithFormats(".ini"),
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

func TestWithFormatsEmptyArgRejectsDiscovery(t *testing.T) {
	dir := t.TempDir()

	appDir := filepath.Join(dir, "myapp")
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	tomlPath := filepath.Join(appDir, "config.toml")
	if err := os.WriteFile(tomlPath, []byte("port = 8080\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)

	_, err := strata.Load[defaultedFormatsConfig](
		strata.WithAppName("myapp"),
		strata.WithFormats(),
	)
	if !errors.Is(err, strata.ErrNoFormats) {
		t.Fatalf("Load WithFormats() error = %v, want ErrNoFormats", err)
	}
}

func TestWithFormatsWhitespaceAndCase(t *testing.T) {
	t.Parallel()

	tomlPath := writeFile(t, "config.toml", "port = 8080\n")

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithPath(tomlPath),
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

func TestStdinTableFirstTOML(t *testing.T) {
	t.Parallel()

	type serverCfg struct {
		Server struct {
			Port int `strata:"port"`
		} `strata:"server"`
	}

	buf := strings.NewReader("[server]\nport = 9000\n")

	cfg, err := strata.Load[serverCfg](
		strata.WithPath("-"),
		strata.WithStdin(buf),
		strata.WithFormats("toml"),
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

	cfg, err := strata.Load[simpleCfg](
		strata.WithPath("-"),
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

	_, err := strata.Load[simpleCfg](
		strata.WithPath(ymlPath),
		strata.WithFormats(".yaml"),
	)
	if err == nil {
		t.Fatal("expected error for explicit .yml path with only .yaml enabled, got nil")
	}

	if !errors.Is(err, strata.ErrUnsupportedFormat) {
		t.Errorf("expected ErrUnsupportedFormat, got %v", err)
	}
}
