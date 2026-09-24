package strata_test

import (
	"errors"
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

	meta.AddActiveFile("/home/user/.config/myapp/config.toml")

	if len(meta.ActiveFiles()) != 1 {
		t.Errorf("ActiveFiles len = %d, want 1", len(meta.ActiveFiles()))
	}
}

func TestMetadataUnicodeKeyNormalization(t *testing.T) {
	t.Parallel()

	meta := strata.NewMetadata()
	meta.Record(strata.Origin{
		Key:      "ÉTAGÈRE",
		Source:   strata.SourceEnv,
		Path:     "ETAGERE",
		RawValue: "val",
	})

	if _, ok := meta.Where("étagère"); !ok {
		t.Errorf("Where('étagère') failed to find key recorded as 'ÉTAGÈRE'")
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

// A zero-value Metadata MUST be usable. Recording into one once panicked with
// "assignment to entry in nil map".
func TestMetadataZeroValueRecordsAndReports(t *testing.T) {
	t.Parallel()

	var meta strata.Metadata

	meta.Record(strata.Origin{Key: "Port", Source: strata.SourceEnv, RawValue: "9090"})

	origin, ok := meta.Where("port")
	if !ok {
		t.Fatal("Where(port) missed after recording on a zero-value Metadata")
	}

	if origin.Source != strata.SourceEnv || origin.RawValue != "9090" {
		t.Fatalf("origin = %+v, want the recorded env origin", origin)
	}

	meta.AddActiveFile("/etc/app/config.toml")

	if files := meta.ActiveFiles(); len(files) != 1 || files[0] != "/etc/app/config.toml" {
		t.Fatalf("ActiveFiles() = %v, want the one recorded path", files)
	}
}

func TestMetadataActiveFilesIsACopy(t *testing.T) {
	t.Parallel()

	meta := strata.NewMetadata()
	meta.AddActiveFile("/original.toml")

	files := meta.ActiveFiles()
	files[0] = "/mutated.toml"

	if got := meta.ActiveFiles(); got[0] != "/original.toml" {
		t.Fatalf("ActiveFiles() = %v, want the returned slice to be a copy", got)
	}
}

// Provenance MUST report a number exactly as the document wrote it. JSON layers
// once routed numbers through float64, rendering 9007199254740993 as
// 9.007199254740992e+15.
func TestProvenancePreservesExactIntegers(t *testing.T) {
	t.Parallel()

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

// Provenance MUST be recorded by a reader for the format that decoded the layer.
// It once ran its own detection cascade instead, so a JSON document read from
// standard input was reported by the YAML reader.
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

// A zero-valued default under a nil nested pointer still gets an origin, so
// `config show` lists it.
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

	if !slices.Equal(keys, []string{"host", "port", "api_key", "database.port", "database.max_conns", "labels"}) {
		t.Errorf("keys %v are not in declaration order", keys)
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
