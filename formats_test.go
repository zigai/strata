package strata_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"
	"github.com/zigai/strata/codec"
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
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())

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

	if _, err := strata.Load[defaultedFormatsConfig](strata.WithAppName("myapp"), strata.WithoutFileDiscovery()); err != nil {
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
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())

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
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())

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

	origin, ok := meta.Where(portKey)
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

func TestWithTypedDecoder(t *testing.T) {
	t.Parallel()

	iniPath := writeFile(t, "config.ini", "port=4040\nhost=ini-host\n")

	cfg, meta, err := strata.LoadWithMetadata[formatsTestConfig](
		strata.WithPath(iniPath),
		strata.WithTypedDecoder(".ini", func(data []byte, target *formatsTestConfig) error {
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
		t.Fatalf("Load WithTypedDecoder: %v", err)
	}

	if cfg.Port != 4040 || cfg.Host != "ini-host" {
		t.Fatalf("cfg = %+v, want port 4040 and host ini-host", cfg)
	}

	if len(meta.ActiveFiles()) != 1 || meta.ActiveFiles()[0] != iniPath {
		t.Fatalf("ActiveFiles = %v, want [%s]", meta.ActiveFiles(), iniPath)
	}
}

func TestNilDecodersPanic(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		register func()
	}{
		{"WithDecoder", func() { strata.WithDecoder(".custom", nil) }},
		{"WithTypedDecoder", func() { strata.WithTypedDecoder[formatsTestConfig](".custom", nil) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Fatalf("expected %s(nil) to panic", test.name)
				}
			}()

			test.register()
		})
	}
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

	origin, ok := meta.Where(portKey)
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
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())

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

func TestWithFormatAliasCascadingLayers(t *testing.T) {
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
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())

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
		t.Fatalf("cfg.Host = %q, want user-host (user layer preserved)", cfg.Host)
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

func TestWithTypedDecoderTypeMismatch(t *testing.T) {
	t.Parallel()

	iniPath := writeFile(t, "config.ini", "data")

	var mismatched cascadingConfig

	_, err := strata.LoadInto(
		&mismatched,
		strata.WithPath(iniPath),
		strata.WithTypedDecoder(".ini", func(_ []byte, _ *formatsTestConfig) error {
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

func TestWithTypedDecoderNilTargetAndErrorWrapping(t *testing.T) {
	t.Parallel()

	iniPath := writeFile(t, "config.ini", "bad-data")

	customErr := errors.New("custom parse failure")

	_, err := strata.Load[formatsTestConfig](
		strata.WithPath(iniPath),
		strata.WithTypedDecoder(".ini", func(_ []byte, _ *formatsTestConfig) error {
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
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())

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

type replacingCodec struct{}

func (replacingCodec) Decode(_ []byte, target any) error {
	cfg, ok := target.(*formatsTestConfig)
	if !ok {
		return fmt.Errorf("replacingCodec cannot decode into %T", target)
	}

	cfg.Port = 777

	return nil
}

func (replacingCodec) Encode(any) ([]byte, error) { return []byte("replaced = true\n"), nil }

func TestWithCodecAppliesToOneLoadOnly(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "config.toml", "port = 8080\nhost = 'h'\n")

	replaced, err := strata.Load[formatsTestConfig](strata.WithPath(path), strata.WithFormats("toml"), strata.WithCodec(".toml", replacingCodec{}))
	if err != nil || replaced.Port != 777 {
		t.Fatalf("load with WithCodec = %+v, %v; want the replacing codec's port 777", replaced, err)
	}

	plain, err := strata.Load[formatsTestConfig](strata.WithPath(path), strata.WithFormats("toml"))
	if err != nil || plain != (formatsTestConfig{Port: 8080, Host: "h"}) {
		t.Fatalf("next load = %+v, %v; want the built-in TOML result", plain, err)
	}

	saved := filepath.Join(t.TempDir(), "saved.toml")
	if err := strata.Save(saved, formatsTestConfig{Port: 1, Host: "s"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	template := filepath.Join(t.TempDir(), "init.toml")
	if err := strata.Init[formatsTestConfig](template); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for path, want := range map[string]string{saved: "port = 1\nhost = 's'\n", template: "port = 0\nhost = ''\n"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q from the built-in encoder", filepath.Base(path), data, err, want)
		}
	}
}

type overlayEntry struct {
	A, B int
}

type overlayConfig struct {
	Name    string
	Entry   overlayEntry
	Entries map[string]overlayEntry
	Nested  map[string]map[string]int
	List    []overlayEntry
	Pair    [2]int
	Timeout time.Duration
	Grace   strata.Duration
}

func (c *overlayConfig) SetDefaults() {
	c.Name = "default"
	c.Entry = overlayEntry{A: 1, B: 2}
	c.Entries = map[string]overlayEntry{"kept": {A: 1, B: 2}, "merged": {A: 1, B: 2}}
	c.Nested = map[string]map[string]int{"inner": {"x": 1, "y": 2}}
	c.List = []overlayEntry{{A: 1, B: 2}}
	c.Pair = [2]int{1, 2}
}

func loadOverlay(t *testing.T, name, document string) (overlayConfig, error) {
	t.Helper()

	path := writeFile(t, name, document)

	return strata.Load[overlayConfig](strata.WithPath(path), strata.WithFormats(filepath.Ext(name)))
}

func TestFormatsMergeMapsAndReplaceLists(t *testing.T) {
	t.Parallel()

	want := overlayConfig{
		Name:    "default",
		Entry:   overlayEntry{A: 9, B: 2},
		Entries: map[string]overlayEntry{"kept": {A: 1, B: 2}, "merged": {A: 9, B: 2}, "added": {A: 9, B: 0}},
		Nested:  map[string]map[string]int{"inner": {"x": 9, "y": 2}},
		List:    []overlayEntry{{A: 9, B: 0}},
		Pair:    [2]int{8, 9},
		Timeout: 7 * 24 * time.Hour,
		Grace:   strata.Duration(90 * time.Second),
	}

	for name, document := range map[string]string{
		"config.json": `{"entry": {"a": 9}, "entries": {"merged": {"a": 9}, "added": {"a": 9}}, "nested": {"inner": {"x": 9}},
			"list": [{"a": 9}], "pair": [8, 9], "timeout": "7d", "grace": "1m30s"}`,
		"config.toml": "list = [{a = 9}]\npair = [8, 9]\ntimeout = \"7d\"\ngrace = \"1m30s\"\n[entry]\na = 9\n" +
			"[entries.merged]\na = 9\n[entries.added]\na = 9\n[nested.inner]\nx = 9\n",
		"config.yaml": "entry: {a: 9}\nentries: {merged: {a: 9}, added: {a: 9}}\nnested: {inner: {x: 9}}\n" +
			"list: [{a: 9}]\npair: [8, 9]\ntimeout: 7d\ngrace: 1m30s\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := loadOverlay(t, name, document)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("loaded %+v\nwant   %+v", got, want)
			}
		})
	}
}

func TestFormatsResetNullToZero(t *testing.T) {
	t.Parallel()

	for name, document := range map[string]string{
		"config.json": `{"name": null, "entry": null, "entries": {"merged": null}, "list": null}`,
		"config.yaml": "name: null\nentry: ~\nentries: {merged: null}\nlist:\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := loadOverlay(t, name, document)
			if err != nil {
				t.Fatal(err)
			}

			wantEntries := map[string]overlayEntry{"kept": {A: 1, B: 2}, "merged": {}}
			if got.Name != "" || got.Entry != (overlayEntry{}) || !reflect.DeepEqual(got.Entries, wantEntries) || got.List != nil {
				t.Fatalf("loaded %+v, want null fields reset to zero and the kept entry untouched", got)
			}
		})
	}
}

func TestFormatsRejectTheSameInvalidValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, document string
		key            string
	}{
		{name: "config.json", document: `{"pair": [1]}`, key: "pair"},
		{name: "config.toml", document: "pair = [1]\n", key: "pair"},
		{name: "config.yaml", document: "pair: [1]\n", key: "pair"},
		{name: "config.json", document: `{"timeout": 30}`, key: "timeout"},
		{name: "config.toml", document: "timeout = 30\n", key: "timeout"},
		{name: "config.yaml", document: "timeout: 30\n", key: "timeout"},
	} {
		t.Run(test.name+" "+test.key, func(t *testing.T) {
			t.Parallel()

			_, err := loadOverlay(t, test.name, test.document)

			var keyErr *codec.KeyError
			if !errors.As(err, &keyErr) || keyErr.Key != test.key || !errors.Is(err, strata.ErrMalformed) {
				t.Fatalf("err = %v, want a malformed-document error for key %q", err, test.key)
			}
		})
	}
}

type inlineServer struct {
	Host string
	Port int
}

type inlineTLS struct {
	CertFile string
}

type inlineConfig struct {
	Server inlineServer `yaml:",inline"`
	TLS    *inlineTLS   `json:",embed"`
	Name   string
	Port   int
}

func (c *inlineConfig) SetDefaults() {
	c.Server.Host = "localhost"
	c.Server.Port = 1
	c.Port = 2
}

func TestInlineFieldsLoadUnderTheParentKeys(t *testing.T) {
	t.Parallel()

	for name, document := range map[string]string{
		"config.json": `{"host": "file", "port": 9, "cert_file": "c.pem"}`,
		"config.toml": "host = 'file'\nport = 9\ncert_file = 'c.pem'\n",
		"config.yaml": "host: file\nport: 9\ncert_file: c.pem\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, name, document)

			got, meta, err := strata.LoadWithMetadata[inlineConfig](strata.WithPath(path), strata.WithFormats(filepath.Ext(name)), strata.WithStrict())
			if err != nil {
				t.Fatal(err)
			}

			want := inlineConfig{Server: inlineServer{Host: "file", Port: 1}, TLS: &inlineTLS{CertFile: "c.pem"}, Port: 9}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("loaded %+v, want %+v", got, want)
			}

			for key, source := range map[string]strata.SourceKind{"host": strata.SourceFile, "port": strata.SourceFile, "cert_file": strata.SourceFile, "name": strata.SourceDefault} {
				if origin, found := meta.Where(key); !found || origin.Source != source {
					t.Errorf("Where(%q) = %+v, %t; want %v", key, origin, found, source)
				}
			}

			if _, found := meta.Where("server.host"); found {
				t.Error("the inline struct kept its own key")
			}
		})
	}
}

func TestInlinePointerStaysNilUntilUsed(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "config.yaml", "host: file\n")

	got, err := strata.Load[inlineConfig](strata.WithPath(path), strata.WithFormats(yamlFormat))
	if err != nil {
		t.Fatal(err)
	}

	if got.TLS != nil {
		t.Fatalf("TLS = %+v, want nil without any of its keys", got.TLS)
	}
}

func TestInlineFieldsRoundTripThroughEveryFormat(t *testing.T) {
	t.Parallel()

	saved := inlineConfig{Server: inlineServer{Host: "saved", Port: 1}, TLS: &inlineTLS{CertFile: "c.pem"}, Name: "n", Port: 7}

	for _, format := range []string{jsonFormat, tomlFormat, yamlFormat} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			savePath := filepath.Join(dir, "saved."+format)

			if err := strata.Save(savePath, saved); err != nil {
				t.Fatal(err)
			}

			got, err := strata.Load[inlineConfig](strata.WithPath(savePath), strata.WithFormats(format), strata.WithStrict())
			if err != nil || !reflect.DeepEqual(got, saved) {
				t.Fatalf("Save then Load = %+v, %v; want %+v", got, err, saved)
			}

			initPath := filepath.Join(dir, "init."+format)
			if err := strata.Init[inlineConfig](initPath); err != nil {
				t.Fatal(err)
			}

			got, err = strata.Load[inlineConfig](strata.WithPath(initPath), strata.WithFormats(format), strata.WithStrict())

			want := inlineConfig{Server: inlineServer{Host: "localhost", Port: 1}, Port: 2}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("Init then Load = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

type inlineMapConfig struct {
	Name  string
	Extra map[string]int `strata:",inline"`
}

func TestInlineMapCollectsUnclaimedKeys(t *testing.T) {
	t.Parallel()

	for name, document := range map[string]string{
		"config.json": `{"name": "n", "alpha": 1, "beta": 2}`,
		"config.toml": "name = 'n'\nalpha = 1\nbeta = 2\n",
		"config.yaml": "name: n\nalpha: 1\nbeta: 2\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, name, document)

			got, meta, err := strata.LoadWithMetadata[inlineMapConfig](strata.WithPath(path), strata.WithFormats(filepath.Ext(name)), strata.WithStrict())
			if err != nil {
				t.Fatal(err)
			}

			want := inlineMapConfig{Name: "n", Extra: map[string]int{"alpha": 1, "beta": 2}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("loaded %+v, want %+v", got, want)
			}

			if origin, found := meta.Where("beta"); !found || origin.RawValue != "2" {
				t.Fatalf("Where(/beta) = %+v, %t", origin, found)
			}
		})
	}
}

func TestInlineMapRejectsValuesOfTheWrongType(t *testing.T) {
	t.Parallel()

	for name, document := range map[string]string{
		"config.json": `{"alpha": "nope"}`,
		"config.toml": "alpha = 'nope'\n",
		"config.yaml": "alpha: nope\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, name, document)

			_, err := strata.Load[inlineMapConfig](strata.WithPath(path), strata.WithFormats(filepath.Ext(name)))

			var keyErr *codec.KeyError
			if !errors.As(err, &keyErr) || keyErr.Key != "alpha" || !errors.Is(err, strata.ErrMalformed) {
				t.Fatalf("err = %v, want a malformed-document error for alpha", err)
			}
		})
	}
}

type inlineOverlapConfig struct {
	Host  string
	Extra map[string]string `strata:",inline"`
}

type inlineSecretMapConfig struct {
	Host  string
	Extra map[string]string `strata:",inline,secret"`
}

type inlineSecretFieldConfig struct {
	Host  string            `strata:",secret"`
	Extra map[string]string `strata:",inline"`
}

func TestInlineMapKeysNeverCollideWithDeclaredFields(t *testing.T) {
	t.Parallel()

	const spacedHost = " host "

	documents := map[string]string{
		"config.json": `{"host": "declared", "HOST": "upper", " host ": "spaced", "ho_st": "underscored"}`,
		"config.toml": "host = 'declared'\nHOST = 'upper'\n' host ' = 'spaced'\nho_st = 'underscored'\n",
		"config.yaml": "host: declared\nHOST: upper\n' host ': spaced\nho_st: underscored\n",
	}
	wantExtra := map[string]string{"HOST": "upper", spacedHost: "spaced", "ho_st": "underscored"}
	wantKeys := []string{"host", spacedHost, "HOST", "ho_st"}

	for name, document := range documents {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, name, document)

			got, meta, err := strata.LoadWithMetadata[inlineOverlapConfig](strata.WithPath(path), strata.WithFormats(filepath.Ext(name)), strata.WithStrict())
			if err != nil {
				t.Fatal(err)
			}

			if got.Host != "declared" || !reflect.DeepEqual(got.Extra, wantExtra) {
				t.Fatalf("loaded %+v, want host declared and extra %q", got, wantExtra)
			}

			keys := make([]string, 0, len(wantKeys))
			for _, origin := range meta.Origins() {
				keys = append(keys, origin.Key)
			}

			slices.Sort(keys)

			if want := slices.Sorted(slices.Values(wantKeys)); !slices.Equal(keys, want) {
				t.Errorf("origin keys = %q, want %q", keys, want)
			}

			for key, raw := range map[string]string{"host": "declared", "HOST": "upper", spacedHost: "spaced", "ho_st": "underscored"} {
				if origin, found := meta.Where(key); !found || origin.RawValue != raw {
					t.Errorf("Where(%q) = %+v, %t; want raw value %q", key, origin, found, raw)
				}
			}

			if origin, found := meta.Where("Host"); found {
				t.Errorf("Where(/Host) = %+v, want no match where an inline map may hold the key", origin)
			}
		})
	}
}

func TestInlineMapSecretsFollowExactKeys(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "config.yaml", "host: declared\nHOST: upper\n")

	_, secretMap, err := strata.LoadWithMetadata[inlineSecretMapConfig](strata.WithPath(path), strata.WithFormats(yamlFormat), strata.WithStrict())
	if err != nil {
		t.Fatal(err)
	}

	_, secretField, err := strata.LoadWithMetadata[inlineSecretFieldConfig](strata.WithPath(path), strata.WithFormats(yamlFormat), strata.WithStrict())
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		meta *strata.Metadata
		key  string
		want string
	}{
		{"secret map, declared key", secretMap, "host", "declared"},
		{"secret map, map key", secretMap, "HOST", "[REDACTED]"},
		{"secret field, declared key", secretField, "host", "[REDACTED]"},
		{"secret field, map key", secretField, "HOST", "upper"},
	} {
		if origin, found := test.meta.Where(test.key); !found || origin.RawValue != test.want {
			t.Errorf("%s: Where(%q) = %+v, %t; want raw value %q", test.name, test.key, origin, found, test.want)
		}
	}
}

type recursiveTree []recursiveTree

type recursiveTreeConfig struct {
	Root  recursiveTree
	Index map[string]recursiveTree
}

func TestRecursiveContainerTypesSaveAndLoad(t *testing.T) {
	t.Parallel()

	want := recursiveTreeConfig{Root: recursiveTree{{}, {{}}}, Index: map[string]recursiveTree{"a": {{}}}}

	for _, format := range []string{jsonFormat, tomlFormat, yamlFormat} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config."+format)
			if err := strata.Save(path, want); err != nil {
				t.Fatalf("Save: %v", err)
			}

			got, err := strata.Load[recursiveTreeConfig](strata.WithPath(path), strata.WithFormats(format))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("loaded %+v, want %+v", got, want)
			}
		})
	}
}

type lowerCaseKey string

var errUpperCaseKey = errors.New("key has upper-case letters")

func (k *lowerCaseKey) UnmarshalText(text []byte) error {
	if strings.ToLower(string(text)) != string(text) {
		return fmt.Errorf("%w: %q", errUpperCaseKey, text)
	}

	*k = lowerCaseKey(text)

	return nil
}

type lowerCaseKeyConfig struct {
	Labels map[lowerCaseKey]int
}

func TestMapKeysDecodeThroughTheirTextUnmarshaler(t *testing.T) {
	t.Parallel()

	for name, document := range map[string]string{
		"config.json": `{"labels": {"Upper": 1}}`,
		"config.toml": "[labels]\nUpper = 1\n",
		"config.yaml": "labels:\n  Upper: 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, name, document)

			got, err := strata.Load[lowerCaseKeyConfig](strata.WithPath(path), strata.WithFormats(filepath.Ext(name)))
			if !errors.Is(err, errUpperCaseKey) {
				t.Fatalf("Load = %+v, %v; want the key rejected by UnmarshalText", got, err)
			}
		})
	}
}
