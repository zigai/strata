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

// A YAML key that only holds an anchor for other keys to reuse sets nothing, so
// it is neither reported as unknown nor rejected in strict mode. An anchor that
// nothing reuses is still treated as a typo.
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
