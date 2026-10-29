package strata_test

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/strata"
)

type nestedPrimitiveConfig struct {
	Nested *struct {
		Enabled bool
		Token   string `strata:",secret"`
	}
}

type mixedCaseConfig struct {
	Alpha    int
	MaxConns int `strata:"maxConns"`
	Zeta     int
}

type stdinFormatConfig struct {
	Alpha int `strata:"alpha"`
}

type customKeyFileConfig struct {
	APIKey string `strata:"apiKey" json:"apiKey"`
}

func (c *customKeyFileConfig) SetDefaults() {
	c.APIKey = "default"
}

func TestProvenanceMetadata(t *testing.T) {
	t.Parallel()

	meta := strata.NewMetadata()

	meta.Record(strata.Origin{
		Key:      "database.port",
		Source:   strata.SourceDefault,
		Path:     "",
		Line:     0,
		RawValue: "5432",
	})

	meta.Record(strata.Origin{
		Key:      "database.port",
		Source:   strata.SourceUser,
		Path:     "/home/user/.config/myapp/config.toml",
		Line:     12,
		RawValue: "5433",
	})

	meta.Record(strata.Origin{
		Key:      "database.port",
		Source:   strata.SourceEnv,
		Path:     "MYAPP_DATABASE__PORT",
		Line:     0,
		RawValue: "9999",
	})

	latest, ok := meta.Where("database.port")
	if !ok {
		t.Fatalf("Where(database.port) not found")
	}

	if latest.Source != strata.SourceEnv || latest.RawValue != "9999" {
		t.Errorf("latest origin = %+v, want SourceEnv 9999", latest)
	}

	meta.RecordActiveFile("/home/user/.config/myapp/config.toml")

	if len(meta.ActiveFiles()) != 1 {
		t.Errorf("ActiveFiles len = %d, want 1", len(meta.ActiveFiles()))
	}
}

func TestWhereFoldsDeclaredUnicodeKeys(t *testing.T) {
	t.Parallel()

	type config struct {
		Shelf string `strata:"ÉTAGÈRE"`
		Delta string `strata:"ǅelta"`
	}

	_, meta, err := strata.LoadWithMetadata[config]()
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"étagère", "ǆelta"} {
		if _, ok := meta.Where(key); !ok {
			t.Errorf("Where(%q) did not find the declared key it folds to", key)
		}
	}
}

func TestWhereMatchesUndeclaredKeysExactly(t *testing.T) {
	t.Parallel()

	meta := strata.NewMetadata()
	meta.Record(strata.Origin{Key: "ÉTAGÈRE", Source: strata.SourceEnv, Path: "ETAGERE", RawValue: "val"})

	if _, ok := meta.Where("ÉTAGÈRE"); !ok {
		t.Error("Where missed the exactly recorded key")
	}

	if origin, ok := meta.Where("étagère"); ok {
		t.Errorf("Where(/étagère) = %+v, want no match for a key no type declares", origin)
	}
}

func TestChainedJSONAliasPreservesSchemaAndProvenance(t *testing.T) {
	t.Parallel()

	cfg, meta, err := strata.LoadWithMetadata[stdinFormatConfig](
		strata.WithPath("-"), strata.WithStdin(strings.NewReader(`{"$schema":"https://example.invalid/config.json","alpha":17}`)),
		strata.WithFormats("settings"), strata.WithFormatAlias("settings", "intermediate"), strata.WithFormatAlias("intermediate", "json"), strata.WithStrict(),
	)
	if err != nil {
		t.Fatal(err)
	}

	origin, found := meta.Where("alpha")
	if cfg.Alpha != 17 || !found || origin.Source != strata.SourceStdin || origin.Line != 0 || origin.RawValue != "17" {
		t.Fatalf("config = %+v, origin = %+v, found = %t", cfg, origin, found)
	}
}

func TestArrayReplacementRemovesDescendantOrigins(t *testing.T) {
	type endpoint struct {
		Port int
	}

	type config struct {
		Endpoints []endpoint
		Label     string
	}

	for _, test := range []struct{ format, lower, empty, shorter string }{
		{"json", `{"label":"retained","endpoints":[{"port":9000},{"port":9001}]}`, `{"endpoints":[]}`, `{"endpoints":[{"port":8000}]}`},
		{"yaml", "label: retained\nendpoints: [{port: 9000}, {port: 9001}]\n", "endpoints: []\n", "endpoints: [{port: 8000}]\n"},
		{"toml", "label = 'retained'\nendpoints = [{port = 9000}, {port = 9001}]\n", "endpoints = []\n", "endpoints = [{port = 8000}]\n"},
	} {
		t.Run(test.format, func(t *testing.T) {
			systemBase, userBase := isolateLayers(t)
			t.Setenv("APPDATA", userBase)
			t.Setenv("ProgramData", systemBase)

			directory := filepath.Join(userBase, "sample")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}

			lowerPath := filepath.Join(directory, "config."+test.format)
			if err := os.WriteFile(lowerPath, []byte(test.lower), 0o600); err != nil {
				t.Fatal(err)
			}

			for _, replacement := range []struct {
				name, body string
				ports      []int
			}{
				{"empty", test.empty, nil},
				{"shorter", test.shorter, []int{8000}},
			} {
				t.Run(replacement.name, func(t *testing.T) {
					upperPath := writeFile(t, "override."+test.format, replacement.body)

					got, meta, err := strata.LoadWithMetadata[config](strata.WithAppName("sample"), strata.WithFormats(test.format), strata.WithPath(upperPath))
					if err != nil {
						t.Fatal(err)
					}

					if len(got.Endpoints) != len(replacement.ports) || got.Label != "retained" {
						t.Fatalf("config = %+v", got)
					}

					if origin, found := meta.Where("endpoints.1.port"); found {
						t.Errorf("removed endpoint retained origin: %+v", origin)
					}

					if len(replacement.ports) == 0 {
						if origin, found := meta.Where("endpoints.0.port"); found {
							t.Errorf("empty array retained origin: %+v", origin)
						}
					} else if origin, found := meta.Where("endpoints.0.port"); got.Endpoints[0].Port != 8000 || !found || origin.Path != upperPath || origin.RawValue != "8000" {
						t.Errorf("replacement origin = %+v, %t; config = %+v", origin, found, got)
					}

					for _, origin := range meta.Origins() {
						if strings.HasPrefix(origin.Key, "endpoints.") && (len(replacement.ports) == 0 || origin.Key != "endpoints.0.port") {
							t.Errorf("Origins retained removed endpoint: %+v", origin)
						}
					}

					if origin, found := meta.Where("label"); !found || origin.Path != lowerPath || origin.RawValue != "retained" {
						t.Errorf("unrelated origin = %+v, %t", origin, found)
					}
				})
			}
		})
	}
}

func TestYAMLExplicitArrayOverridesMergedOrigins(t *testing.T) {
	t.Parallel()

	type config struct {
		Endpoints []struct{ Port int }
	}

	got, meta, err := strata.LoadWithMetadata[config](strata.WithPath("-"), strata.WithFormats("yaml"), strata.WithStdin(strings.NewReader("base: &base\n  endpoints: [{port: 9000}]\n<<: *base\nendpoints: []\n")))
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Endpoints) != 0 {
		t.Fatalf("endpoints = %+v, want empty", got.Endpoints)
	}

	if origin, found := meta.Where("endpoints.0.port"); found {
		t.Fatalf("overridden endpoint retained origin: %+v", origin)
	}
}

func TestYAMLAliasAndSequenceProvenance(t *testing.T) {
	t.Parallel()

	type config struct {
		Database struct{ Port int } `strata:"database"`
		Ports    []int              `strata:"ports"`
	}

	cfg, meta, err := strata.LoadWithMetadata[config](strata.WithPath("-"), strata.WithStdin(strings.NewReader("base: &base\n  port: 9001\ndatabase: *base\nports: [81, 444]\n")), strata.WithFormats("yaml"), strata.WithStrict())
	if err != nil {
		t.Fatal(err)
	}

	origin, found := meta.Where("database.port")
	if cfg.Database.Port != 9001 || !found || origin.Source != strata.SourceStdin || origin.RawValue != "9001" {
		t.Fatalf("config = %+v, alias origin = %+v, %t", cfg, origin, found)
	}

	ports, found := meta.Where("ports")
	if !found || !strings.Contains(ports.RawValue, "444") {
		t.Fatalf("ports origin = %+v, %t", ports, found)
	}
}

func TestConfigErrorDiagnostics(t *testing.T) {
	t.Parallel()

	meta := strata.NewMetadata()

	meta.Record(strata.Origin{
		Key:      "tracker.interval",
		Source:   strata.SourceEnv,
		Path:     "AHT_TRACKER_INTERVAL",
		Line:     0,
		RawValue: "invalid",
	})

	rootErr := errors.New("invalid duration")
	cfgErr := meta.NewConfigError("tracker.interval", rootErr)

	if !errors.Is(cfgErr, rootErr) {
		t.Errorf("errors.Is(cfgErr, rootErr) = false, want true")
	}

	errStr := cfgErr.Error()

	wantLines := []string{
		"config error: invalid duration for tracker.interval: \"invalid\"",
		"--> set by environment variable: AHT_TRACKER_INTERVAL",
	}

	for _, wantLine := range wantLines {
		if !strings.Contains(errStr, wantLine) {
			t.Errorf("Error string %q does not contain %q", errStr, wantLine)
		}
	}
}

// Regression: recording into zero-value Metadata once panicked on a nil map.
func TestMetadataZeroValueRecordsAndReports(t *testing.T) {
	t.Parallel()

	var meta strata.Metadata

	meta.Record(strata.Origin{Key: portKey, Source: strata.SourceEnv, RawValue: "9090"})

	origin, ok := meta.Where(portKey)
	if !ok {
		t.Fatal("Where(port) missed after recording on a zero-value Metadata")
	}

	if origin.Source != strata.SourceEnv || origin.RawValue != "9090" {
		t.Fatalf("origin = %+v, want the recorded env origin", origin)
	}

	meta.RecordActiveFile("/etc/app/config.toml")

	if files := meta.ActiveFiles(); len(files) != 1 || files[0] != "/etc/app/config.toml" {
		t.Fatalf("ActiveFiles() = %v, want the one recorded path", files)
	}
}

func TestMetadataActiveFilesIsACopy(t *testing.T) {
	t.Parallel()

	meta := strata.NewMetadata()
	meta.RecordActiveFile("/original.toml")

	files := meta.ActiveFiles()
	files[0] = "/mutated.toml"

	if got := meta.ActiveFiles(); got[0] != "/original.toml" {
		t.Fatalf("ActiveFiles() = %v, want the returned slice to be a copy", got)
	}
}

// Regression: float64 conversion rounded 9007199254740993.
func TestProvenancePreservesExactIntegers(t *testing.T) {
	t.Parallel()

	type wideConfig struct {
		Small int64 `strata:"small" json:"small" toml:"small" yaml:"small"`
	}

	for _, tc := range []struct {
		ext  string
		body string
	}{
		{".json", `{"small": 9007199254740993}`},
		{".toml", "small = 9007199254740993\n"},
		{".yaml", "small: 9007199254740993\n"},
	} {
		t.Run(tc.ext, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "cfg"+tc.ext)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("seed: %v", err)
			}

			_, meta, err := strata.LoadWithMetadata[wideConfig](strata.WithPath(path), strata.WithFormats(tc.ext))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			origin, ok := meta.Where("small")
			if !ok {
				t.Fatal("no origin recorded")
			}

			if origin.RawValue != "9007199254740993" {
				t.Errorf("RawValue = %q, want the literal 9007199254740993", origin.RawValue)
			}
		})
	}
}

// Regression: format auto-detection attributed JSON stdin to the YAML reader.
func TestProvenanceFollowsStdinFormat(t *testing.T) {
	t.Parallel()

	t.Run("json", func(t *testing.T) {
		t.Parallel()

		_, meta, err := strata.LoadWithMetadata[stdinFormatConfig](
			strata.WithPath("-"),
			strata.WithStdin(strings.NewReader(`{"alpha": 7}`)),
			strata.WithFormats("json"),
		)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		origin, ok := meta.Where("alpha")
		if !ok {
			t.Fatal("Where(alpha) not found")
		}

		if origin.Line != 0 {
			t.Errorf("origin.Line = %d, want 0: the JSON reader reports no line", origin.Line)
		}
	})

	t.Run("yaml", func(t *testing.T) {
		t.Parallel()

		_, meta, err := strata.LoadWithMetadata[stdinFormatConfig](
			strata.WithPath("-"),
			strata.WithStdin(strings.NewReader("alpha: 7\n")),
			strata.WithFormats("yaml"),
		)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		origin, ok := meta.Where("alpha")
		if !ok {
			t.Fatal("Where(alpha) not found")
		}

		if origin.Line != 1 {
			t.Errorf("origin.Line = %d, want 1: the YAML reader reports the line", origin.Line)
		}
	})
}

func TestCustomKeyFileProvenance(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	filePath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(filePath, []byte(`{"apiKey":"file-value"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, meta, err := strata.LoadWithMetadata[customKeyFileConfig](strata.WithPath(filePath), strata.WithFormats("json"))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	o, ok := meta.Where("apiKey")
	if !ok {
		t.Fatal("missing origin")
	}

	if o.Source != strata.SourceFile || o.RawValue != "file-value" {
		t.Errorf("file override provenance stays stale: %+v", o)
	}
}

func TestNestedZeroDefaultsHaveOrigins(t *testing.T) {
	_, meta, err := strata.LoadWithMetadata[nestedPrimitiveConfig]()
	if err != nil {
		t.Fatal(err)
	}

	if origin, ok := meta.Where("nested.enabled"); !ok || origin.RawValue != "false" {
		t.Fatalf("enabled origin = %+v, %t", origin, ok)
	}
}

func TestMixedCaseOriginsFollowDeclarationOrder(t *testing.T) {
	_, meta, err := strata.LoadWithMetadata[mixedCaseConfig]()
	if err != nil {
		t.Fatal(err)
	}

	origins := meta.Origins()
	keys := make([]string, 0, len(origins))

	for _, origin := range origins {
		keys = append(keys, origin.Key)
	}

	if !reflect.DeepEqual(keys, []string{"alpha", "maxConns", "zeta"}) {
		t.Fatalf("origin order = %v", keys)
	}
}

func TestOriginsListsEveryResolvedKey(t *testing.T) {
	t.Setenv("ORIGINS_HOST", "from-env")

	path := writeFile(t, "config.toml", "[database]\nport = 5432\n")

	_, meta, err := strata.LoadWithMetadata[typoConfig](strata.WithPath(path), strata.WithEnvPrefix("ORIGINS_"), strata.WithFormats("toml"))
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

	if !slices.Equal(keys, []string{"host", portKey, "api_key", "database.port", "database.max_conns", "labels"}) {
		t.Errorf("keys %v are not in declaration order", keys)
	}

	want := map[string]strata.SourceKind{
		"host":          strata.SourceEnv,
		portKey:         strata.SourceDefault,
		"database.port": strata.SourceFile,
	}

	for key, source := range want {
		if sources[key] != source {
			t.Errorf("source of %s = %q, want %q (all: %v)", key, sources[key], source, sources)
		}
	}
}

func TestWhereReportsTheExactOriginPerSource(t *testing.T) {
	tomlPath := writeFile(t, "c.toml", "host = 'h'\nport = 9000\n")
	yamlPath := writeFile(t, "c.yaml", "host: h\nlabels:\n  env: prod\n")

	for _, tc := range []struct {
		name string
		opts []strata.Option
		env  map[string]string
		key  string
		want strata.Origin
	}{
		{
			"default", nil, nil, portKey,
			strata.Origin{Key: portKey, Source: strata.SourceDefault, RawValue: "8080"},
		},
		{
			"toml file",
			[]strata.Option{strata.WithPath(tomlPath), strata.WithFormats("toml")},
			nil, portKey,
			strata.Origin{Key: portKey, Source: strata.SourceFile, Path: tomlPath, RawValue: "9000"},
		},
		{
			"yaml map entry",
			[]strata.Option{strata.WithPath(yamlPath), strata.WithFormats("yaml")},
			nil, "labels.env",
			strata.Origin{Key: "labels.env", Source: strata.SourceFile, Path: yamlPath, Line: 3, RawValue: "prod"},
		},
		{
			"env",
			[]strata.Option{strata.WithEnvPrefix("PROV_")},
			map[string]string{"PROV_DATABASE_PORT": "6000"},
			"database.port",
			strata.Origin{Key: "database.port", Source: strata.SourceEnv, Path: "PROV_DATABASE_PORT", RawValue: "6000"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, value := range tc.env {
				t.Setenv(name, value)
			}

			_, meta, err := strata.LoadWithMetadata[typoConfig](tc.opts...)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if got, ok := meta.Where(tc.key); !ok || got != tc.want {
				t.Fatalf("Where(%s) = %+v, %t; want %+v", tc.key, got, ok, tc.want)
			}
		})
	}
}

func TestMapEntriesWithSurroundingSpacesKeepTheirOwnOrigins(t *testing.T) {
	t.Parallel()

	const spacedLabel = " key "

	type config struct {
		Port   int
		Labels map[string]string
	}

	stdin := strings.NewReader(`{"port": 8080, "labels": {"key": "one", " key ": "two"}}`)

	loaded, meta, err := strata.LoadWithMetadata[config](strata.WithStdin(stdin), strata.WithPath("-"), strata.WithFormats("json"))
	if err != nil {
		t.Fatal(err)
	}

	if want := map[string]string{"key": "one", spacedLabel: "two"}; !maps.Equal(loaded.Labels, want) {
		t.Fatalf("Labels = %q, want %q", loaded.Labels, want)
	}

	for _, test := range []struct {
		key   string
		value string
	}{
		{key: "labels.key", value: "one"},
		{key: "labels." + spacedLabel, value: "two"},
		{key: " port ", value: "8080"},
		{key: "labels .key", value: "one"},
	} {
		origin, found := meta.Where(test.key)
		if !found || origin.RawValue != test.value {
			t.Errorf("Where(%q) = %+v, %t; want %q", test.key, origin, found, test.value)
		}
	}

	origins := meta.Origins()

	keys := make([]string, 0, len(origins))
	for _, origin := range origins {
		keys = append(keys, origin.Key)
	}

	if want := []string{"port", "labels", "labels." + spacedLabel, "labels.key"}; !slices.Equal(keys, want) {
		t.Errorf("origin keys = %q, want %q", keys, want)
	}
}

func TestMapEntriesSpelledAlikeKeepTheirOwnOrigins(t *testing.T) {
	t.Parallel()

	type config struct {
		Labels map[string]string
	}

	path := writeFile(t, "config.yaml", "labels:\n  foo_bar: one\n  foobar: two\n  Env: three\n  env: four\n")

	_, meta, err := strata.LoadWithMetadata[config](strata.WithPath(path), strata.WithFormats("yaml"))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		key   string
		value string
		line  int
	}{
		{key: "labels.foo_bar", value: "one", line: 2},
		{key: "labels.foobar", value: "two", line: 3},
		{key: "labels.Env", value: "three", line: 4},
		{key: "labels.env", value: "four", line: 5},
		{key: "Labels.env", value: "four", line: 5},
	} {
		origin, found := meta.Where(test.key)
		if !found || origin.RawValue != test.value || origin.Line != test.line {
			t.Errorf("Where(%q) = %+v, %t; want %q from line %d", test.key, origin, found, test.value, test.line)
		}
	}

	if origin, found := meta.Where("labels.ENV"); found {
		t.Errorf("Where(labels.ENV) = %+v, want no match for a spelling two entries share", origin)
	}

	origins := meta.Origins()

	keys := make([]string, 0, len(origins))
	for _, origin := range origins {
		keys = append(keys, origin.Key)
	}

	if want := []string{"labels", "labels.foo_bar", "labels.foobar", "labels.Env", "labels.env"}; !slices.Equal(keys, want) {
		t.Errorf("origin keys = %v, want %v", keys, want)
	}
}

func TestLiteralDottedKeysKeepTheirOwnOrigins(t *testing.T) {
	t.Parallel()

	type config struct {
		Labels map[string]any
	}

	for name, document := range map[string]string{
		"config.json": "{\"labels\": {\n\"a.b\": \"literal\",\n\"a\": {\"b\": \"nested\"}}}",
		"config.toml": "[labels]\n\"a.b\" = \"literal\"\n[labels.a]\nb = \"nested\"\n",
		"config.yaml": "labels:\n  a.b: literal\n  a:\n    b: nested\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, name, document)

			got, meta, err := strata.LoadWithMetadata[config](strata.WithPath(path), strata.WithFormats(filepath.Ext(name)))
			if err != nil {
				t.Fatal(err)
			}

			want := map[string]any{"a.b": "literal", "a": map[string]any{"b": "nested"}}
			if !reflect.DeepEqual(got.Labels, want) {
				t.Fatalf("labels = %v, want %v", got.Labels, want)
			}

			for key, raw := range map[string]string{`labels.a\.b`: "literal", "labels.a.b": "nested"} {
				if origin, found := meta.Where(key); !found || origin.RawValue != raw {
					t.Errorf("Where(%q) = %+v, %t; want raw value %q", key, origin, found, raw)
				}
			}
		})
	}
}
