package strata_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/strata"
)

type typoDatabase struct {
	Port     int
	MaxConns int
}

type typoConfig struct {
	Host     string
	Port     int
	APIKey   string `strata:"api_key,secret"`
	Database typoDatabase
	Labels   map[string]string
}

func (c *typoConfig) SetDefaults() {
	c.Port = 8080
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}

func TestUnknownKeysAreReportedWithSuggestions(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "config.yaml", "prot: 9000\ndatabase:\n  max_con: 5\n  port: 5432\nlabels:\n  anything: goes\napi_kye: hunter2\ncompletely_unrelated: 1\n")

	cfg, meta, err := strata.LoadWithMetadata[typoConfig](strata.WithPath(path))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != 8080 || cfg.Database.Port != 5432 {
		t.Fatalf("cfg = %+v, want default port and database.port 5432", cfg)
	}

	got := make(map[string]strata.UnknownKey)
	for _, unknown := range meta.UnknownKeys() {
		got[unknown.Key] = unknown
	}

	want := map[string]string{
		"prot":                 "port",
		"database.max_con":     "database.max_conns",
		"api_kye":              "api_key",
		"completely_unrelated": "",
	}

	if len(got) != len(want) {
		t.Fatalf("unknown keys = %+v, want %v", got, want)
	}

	for key, suggestion := range want {
		unknown, ok := got[key]
		if !ok {
			t.Fatalf("unknown key %q not reported; got %+v", key, got)
		}

		if unknown.Suggestion != suggestion {
			t.Errorf("suggestion for %q = %q, want %q", key, unknown.Suggestion, suggestion)
		}

		if unknown.Path != path || unknown.Line == 0 || unknown.Source != strata.SourceFile {
			t.Errorf("origin for %q = %+v, want %s with a line", key, unknown.Origin, path)
		}

		if unknown.RawValue != "" {
			t.Errorf("value for %q = %q, want it dropped", key, unknown.RawValue)
		}
	}

	if _, ok := meta.Where("prot"); ok {
		t.Error("Where(prot) found an origin for a key that set nothing")
	}
}

func TestStrictRejectsUnknownKeys(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "config.toml", "prot = 9000\n")

	_, err := strata.Load[typoConfig](strata.WithPath(path), strata.WithStrict())
	if !errors.Is(err, strata.ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey", err)
	}

	configErr, ok := errors.AsType[*strata.ConfigError](err)
	if !ok || configErr.Key != "prot" {
		t.Fatalf("err = %#v, want a ConfigError for prot", err)
	}

	message := err.Error()
	for _, want := range []string{`unknown key "prot"`, `did you mean "port"?`, path} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q lacks %q", message, want)
		}
	}

	if _, err := strata.Load[typoConfig](strata.WithPath(writeFile(t, "ok.toml", "port = 1\n")), strata.WithStrict()); err != nil {
		t.Fatalf("strict load of a valid file: %v", err)
	}
}

func TestOptionalPath(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "absent.toml")

	cfg, err := strata.Load[typoConfig](strata.WithOptionalPath(missing))
	if err != nil || cfg.Port != 8080 {
		t.Fatalf("optional missing file: cfg=%+v err=%v, want defaults", cfg, err)
	}

	if _, err := strata.Load[typoConfig](strata.WithPath(missing)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("required missing file: err = %v, want os.ErrNotExist", err)
	}

	present := writeFile(t, "config.toml", "port = 9000\n")

	cfg, err = strata.Load[typoConfig](strata.WithOptionalPath(present))
	if err != nil || cfg.Port != 9000 {
		t.Fatalf("optional present file: cfg=%+v err=%v, want port 9000", cfg, err)
	}

	broken := writeFile(t, "broken.toml", "port = = 1\n")
	if _, err := strata.Load[typoConfig](strata.WithOptionalPath(broken)); !errors.Is(err, strata.ErrMalformed) {
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

func TestOriginsListsEveryResolvedKey(t *testing.T) {
	t.Setenv("ORIGINS_HOST", "from-env")

	path := writeFile(t, "config.toml", "[database]\nport = 5432\n")

	_, meta, err := strata.LoadWithMetadata[typoConfig](strata.WithPath(path), strata.WithEnvPrefix("ORIGINS_"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	origins := meta.Origins()
	sources := make(map[string]strata.SourceKind, len(origins))
	keys := make([]string, 0, len(origins))

	for _, origin := range origins {
		keys = append(keys, origin.Key)
		sources[origin.Key] = origin.Source
	}

	if !slices.IsSorted(keys) {
		t.Errorf("keys %v are not sorted", keys)
	}

	want := map[string]strata.SourceKind{
		"host":          strata.SourceEnv,
		"port":          strata.SourceDefault,
		"database.port": strata.SourceFile,
	}

	for key, source := range want {
		if sources[key] != source {
			t.Errorf("source of %s = %q, want %q (all: %v)", key, sources[key], source, sources)
		}
	}
}

func TestEnvironmentNeedsPrefixForDerivedNames(t *testing.T) {
	t.Setenv("PORT", "1234")
	t.Setenv("HOST", "from-env")
	t.Setenv("TAGGED_TOKEN", "abc")

	type config struct {
		Host  string
		Port  int
		Token string `env:"TAGGED_TOKEN"`
	}

	cfg, err := strata.Load[config]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Host != "" || cfg.Port != 0 {
		t.Errorf("cfg = %+v, want PORT and HOST ignored without a prefix", cfg)
	}

	if cfg.Token != "abc" {
		t.Errorf("Token = %q, want the explicitly tagged variable to bind", cfg.Token)
	}
}

func TestEnvVars(t *testing.T) {
	t.Parallel()

	vars := strata.EnvVars[typoConfig]("myapp")

	got := make(map[string]strata.EnvVar)
	for _, v := range vars {
		got[v.Key] = v
	}

	tests := []struct {
		key    string
		name   string
		secret bool
	}{
		{key: "host", name: "MYAPP_HOST"},
		{key: "api_key", name: "MYAPP_API_KEY", secret: true},
		{key: "database.max_conns", name: "MYAPP_DATABASE_MAX_CONNS"},
	}

	for _, tt := range tests {
		v, ok := got[tt.key]
		if !ok {
			t.Fatalf("no variable for %s in %+v", tt.key, vars)
		}

		if v.Name != tt.name || v.Secret != tt.secret {
			t.Errorf("%s = %+v, want name %s secret %t", tt.key, v, tt.name, tt.secret)
		}
	}

	if _, ok := got["labels"]; ok {
		t.Error("a map field is listed, but the environment cannot set one")
	}

	if names := got["database.port"].Names; !slices.Equal(names, []string{"MYAPP_DATABASE__PORT", "MYAPP_DATABASE_PORT"}) {
		t.Errorf("database.port names = %v", names)
	}

	if unprefixed := strata.EnvVars[typoConfig](""); len(unprefixed) != 0 {
		t.Errorf("EnvVars without a prefix = %+v, want none for untagged fields", unprefixed)
	}
}

func TestSaveThenSetUsesOneKeySpelling(t *testing.T) {
	t.Parallel()

	for _, ext := range []string{".toml", ".yaml", ".json"} {
		t.Run(ext, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "state"+ext)

			if err := strata.Save(path, typoConfig{Host: "h", Database: typoDatabase{MaxConns: 3}}); err != nil {
				t.Fatalf("Save: %v", err)
			}

			if err := strata.Set(path, "database.max_conns", 9); err != nil {
				t.Fatalf("Set: %v", err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			if strings.Count(string(data), "max_conns") != 1 || strings.Contains(string(data), "MaxConns") {
				t.Fatalf("file names the key more than one way:\n%s", data)
			}

			cfg, err := strata.Load[typoConfig](strata.WithPath(path), strata.WithStrict())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if cfg.Database.MaxConns != 9 || cfg.Host != "h" {
				t.Fatalf("cfg = %+v, want max_conns 9 and host h", cfg)
			}
		})
	}
}
