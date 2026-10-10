package strata_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zigai/strata"
)

func TestKeyName(t *testing.T) {
	t.Parallel()

	field, _ := reflect.TypeFor[primitiveConfig]().FieldByName("Timeout")
	if got := strata.KeyName(field); got != "timeout" {
		t.Fatalf("key = %q", got)
	}
}

func TestUnknownKeysAreReportedWithSuggestions(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "config.yaml", "prot: 9000\ndatabase:\n  max_con: 5\n  port: 5432\nlabels:\n  anything: goes\napi_kye: hunter2\ncompletely_unrelated: 1\n")

	cfg, meta, err := strata.LoadWithMetadata[typoConfig](strata.WithPath(path), strata.WithFormats("yaml"))
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
		"prot":                 portKey,
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

	_, err := strata.Load[typoConfig](strata.WithPath(path), strata.WithStrict(), strata.WithFormats("toml"))
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

	if _, err := strata.Load[typoConfig](strata.WithPath(writeFile(t, "ok.toml", "port = 1\n")), strata.WithStrict(), strata.WithFormats("toml")); err != nil {
		t.Fatalf("strict load of a valid file: %v", err)
	}
}

func TestStrictRejectsUnknownStructuredKeys(t *testing.T) {
	t.Parallel()

	type endpoint struct {
		Port int `strata:"port"`
	}

	type config struct {
		Database  endpoint            `strata:"database"`
		Endpoints []endpoint          `strata:"endpoints"`
		Services  map[string]endpoint `strata:"services"`
	}

	for _, test := range []struct {
		name, format, body string
	}{
		{"empty JSON", jsonFormat, `{"typo":{}}`},
		{"empty TOML", tomlFormat, "[typo]\n"},
		{"empty YAML", yamlFormat, "typo: {}\n"},
		{"dotted JSON", jsonFormat, `{"database.port":9001}`},
		{"dotted TOML", tomlFormat, "\"database.port\" = 9001\n"},
		{"dotted YAML", yamlFormat, "\"database.port\": 9001\n"},
		{"slice JSON", jsonFormat, `{"endpoints":[{"prot":9001}]}`},
		{"slice TOML", tomlFormat, "[[endpoints]]\nprot = 9001\n"},
		{"slice YAML", yamlFormat, "endpoints:\n  - prot: 9001\n"},
		{"map JSON", jsonFormat, `{"services":{"public":{"prot":9001}}}`},
		{"map TOML", tomlFormat, "[services.public]\nprot = 9001\n"},
		{"map YAML", yamlFormat, "services:\n  public:\n    prot: 9001\n"},
		{"alias YAML", yamlFormat, "base: &base\n  prot: 9001\ndatabase: *base\n"},
		{"merge YAML", yamlFormat, "base: &base\n  prot: 9001\ndatabase:\n  <<: *base\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := strata.Load[config](strata.WithPath("-"), strata.WithStdin(strings.NewReader(test.body)), strata.WithFormats(test.format), strata.WithStrict())
			if !errors.Is(err, strata.ErrUnknownKey) {
				t.Fatalf("Load error = %v, want ErrUnknownKey", err)
			}
		})
	}
}

func TestStrictAcceptsStructuredCollectionValues(t *testing.T) {
	t.Parallel()

	type endpoint struct {
		Port int `strata:"port"`
	}

	type config struct {
		Endpoints []endpoint          `strata:"endpoints"`
		Services  map[string]endpoint `strata:"services"`
		Labels    map[string]string   `strata:"labels"`
	}

	for format, body := range map[string]string{
		jsonFormat: `{"endpoints":[{"port":9001}],"services":{"public":{"port":9002}},"labels":{"a.b":"ok"}}`,
		tomlFormat: "endpoints = [{port = 9001}]\n[services.public]\nport = 9002\n[labels]\n\"a.b\" = 'ok'\n",
		yamlFormat: "endpoints:\n  - port: 9001\nservices:\n  public:\n    port: 9002\nlabels:\n  a.b: ok\n",
	} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			cfg, err := strata.Load[config](strata.WithPath("-"), strata.WithStdin(strings.NewReader(body)), strata.WithFormats(format), strata.WithStrict())
			if err != nil {
				t.Fatal(err)
			}

			if len(cfg.Endpoints) != 1 || cfg.Endpoints[0].Port != 9001 || cfg.Services["public"].Port != 9002 || cfg.Labels["a.b"] != "ok" {
				t.Fatalf("config = %+v", cfg)
			}
		})
	}
}

func TestReusedYAMLAnchorsAreNotUnknownKeys(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "config.yaml", "base: &b\n  port: 5432\ndatabase:\n  <<: *b\n")

	cfg, meta, err := strata.LoadWithMetadata[typoConfig](strata.WithPath(path), strata.WithStrict(), strata.WithFormats("yaml"))
	if err != nil {
		t.Fatalf("strict load: %v", err)
	}

	if cfg.Database.Port != 5432 {
		t.Errorf("database.port = %d, want 5432 from the merge", cfg.Database.Port)
	}

	if unknown := meta.UnknownKeys(); len(unknown) != 0 {
		t.Errorf("unknown keys = %v, want none", unknown)
	}

	unused := writeFile(t, "unused.yaml", "base: &b\n  port: 5432\n")

	_, err = strata.Load[typoConfig](strata.WithPath(unused), strata.WithStrict(), strata.WithFormats("yaml"))
	if !errors.Is(err, strata.ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey for an anchor nothing reuses", err)
	}
}

func TestUnknownKeysAreListedInReadOrder(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "config.yaml", "prot: 9000\ndatabase:\n  maxconns: 5\n  port: 5432\nzzz_unrelated: 1\n")

	cfg, meta, err := strata.LoadWithMetadata[typoConfig](strata.WithPath(path), strata.WithFormats("yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Database.MaxConns != 0 {
		t.Fatalf("maxconns set database.max_conns to %d", cfg.Database.MaxConns)
	}

	origin := func(key string, line int) strata.Origin {
		return strata.Origin{Key: key, Source: strata.SourceFile, Path: path, Line: line}
	}

	want := []strata.UnknownKey{
		{Origin: origin("prot", 1), Suggestion: portKey},
		{Origin: origin("database.maxconns", 3), Suggestion: "database.max_conns"},
		{Origin: origin("zzz_unrelated", 5), Suggestion: ""},
	}
	if got := meta.UnknownKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("UnknownKeys =\n%+v\nwant\n%+v", got, want)
	}
}
