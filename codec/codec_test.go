package codec_test

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/zigai/strata/codec"
)

// sampleConfig is the fixture shared by the codec tests. It carries a tag for
// each built-in format, so one value exercises every codec.
type sampleConfig struct {
	Name string   `json:"name" toml:"name" yaml:"name"`
	Port int      `json:"port" toml:"port" yaml:"port"`
	Tags []string `json:"tags" toml:"tags" yaml:"tags"`
}

// TestRegistryDefaults pins the extension order that auto-discovery priority
// depends on, and the presence of a codec behind each default extension.
func TestRegistryDefaults(t *testing.T) {
	t.Parallel()

	reg := codec.NewRegistry()
	exts := reg.Extensions()

	wantExts := []string{".toml", ".yaml", ".yml", ".json"}
	if !reflect.DeepEqual(exts, wantExts) {
		t.Fatalf("Extensions() = %v, want %v", exts, wantExts)
	}

	for _, ext := range wantExts {
		c, ok := reg.Get(ext)
		if !ok || c == nil {
			t.Fatalf("Get(%q) returned false or nil", ext)
		}
	}

	if _, ok := reg.Get(".unknown"); ok {
		t.Fatalf("Get(\".unknown\") returned true, want false")
	}
}

func TestRegistryRestrict(t *testing.T) {
	t.Parallel()

	t.Run("single extension", func(t *testing.T) {
		t.Parallel()

		reg := codec.NewRegistry()
		reg.Restrict(".toml")

		got := reg.Extensions()
		want := []string{".toml"}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Extensions() = %v, want %v", got, want)
		}

		if _, ok := reg.Get(".toml"); !ok {
			t.Fatal("Get(\".toml\") returned false, want true")
		}

		if _, ok := reg.Get(".yaml"); ok {
			t.Fatal("Get(\".yaml\") returned true, want false")
		}

		if _, ok := reg.Get(".json"); ok {
			t.Fatal("Get(\".json\") returned true, want false")
		}
	})

	t.Run("reorder and filter", func(t *testing.T) {
		t.Parallel()

		reg := codec.NewRegistry()
		reg.Restrict(".json", ".toml")

		got := reg.Extensions()
		want := []string{".json", ".toml"}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Extensions() = %v, want %v", got, want)
		}
	})

	t.Run("normalizes extensions", func(t *testing.T) {
		t.Parallel()

		reg := codec.NewRegistry()
		reg.Restrict("TOML", " json ")
		got := reg.Extensions()
		want := []string{".toml", ".json"}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Extensions() = %v, want %v", got, want)
		}
	})

	t.Run("skips unregistered and deduplicates", func(t *testing.T) {
		t.Parallel()

		reg := codec.NewRegistry()
		reg.Restrict(".toml", ".unknown", ".toml", ".yaml")

		got := reg.Extensions()
		want := []string{".toml", ".yaml"}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Extensions() = %v, want %v", got, want)
		}
	})

	t.Run("empty removes all", func(t *testing.T) {
		t.Parallel()

		reg := codec.NewRegistry()
		reg.Restrict()

		got := reg.Extensions()
		if len(got) != 0 {
			t.Fatalf("Extensions() = %v, want empty", got)
		}

		if _, ok := reg.Get(".toml"); ok {
			t.Fatal("Get(\".toml\") returned true, want false")
		}
	})
}

func TestTOMLCodec(t *testing.T) {
	t.Parallel()

	c := codec.NewTOMLCodec()
	input := []byte("name = \"my-app\"\nport = 8080\ntags = [\"web\", \"api\"]\n")

	var target sampleConfig
	if err := c.Decode(input, &target); err != nil {
		t.Fatalf("Decode TOML: %v", err)
	}

	if target.Name != "my-app" || target.Port != 8080 || len(target.Tags) != 2 {
		t.Fatalf("Decoded struct mismatch: %+v", target)
	}

	encoded, err := c.Encode(target)
	if err != nil {
		t.Fatalf("Encode TOML: %v", err)
	}

	var roundtrip sampleConfig
	if err := c.Decode(encoded, &roundtrip); err != nil {
		t.Fatalf("Roundtrip Decode: %v", err)
	}

	if !reflect.DeepEqual(target, roundtrip) {
		t.Fatalf("Roundtrip mismatch: %+v vs %+v", target, roundtrip)
	}
}

func TestYAMLCodec(t *testing.T) {
	t.Parallel()

	c := codec.NewYAMLCodec()
	input := []byte("name: my-app\nport: 8080\ntags:\n  - web\n  - api\n")

	var target sampleConfig
	if err := c.Decode(input, &target); err != nil {
		t.Fatalf("Decode YAML: %v", err)
	}

	if target.Name != "my-app" || target.Port != 8080 || len(target.Tags) != 2 {
		t.Fatalf("Decoded struct mismatch: %+v", target)
	}

	encoded, err := c.Encode(target)
	if err != nil {
		t.Fatalf("Encode YAML: %v", err)
	}

	var roundtrip sampleConfig
	if err := c.Decode(encoded, &roundtrip); err != nil {
		t.Fatalf("Roundtrip Decode: %v", err)
	}

	if !reflect.DeepEqual(target, roundtrip) {
		t.Fatalf("Roundtrip mismatch: %+v vs %+v", target, roundtrip)
	}
}

func TestJSONCodec(t *testing.T) {
	t.Parallel()

	c := codec.NewJSONCodec()
	input := []byte(`{"name":"my-app","port":8080,"tags":["web","api"]}`)

	var target sampleConfig
	if err := c.Decode(input, &target); err != nil {
		t.Fatalf("Decode JSON: %v", err)
	}

	if target.Name != "my-app" || target.Port != 8080 || len(target.Tags) != 2 {
		t.Fatalf("Decoded struct mismatch: %+v", target)
	}

	encoded, err := c.Encode(target)
	if err != nil {
		t.Fatalf("Encode JSON: %v", err)
	}

	// NB: the space after the colon is what pins the indented encoding; a
	// compact encoder would emit "name":"my-app".
	if !strings.Contains(string(encoded), "\"name\": \"my-app\"") {
		t.Fatalf("Encoded JSON should be formatted: %s", string(encoded))
	}
}

// TestCodecNilTarget checks that every codec in the default registry reports an
// error for a nil target rather than panicking.
func TestCodecNilTarget(t *testing.T) {
	t.Parallel()

	reg := codec.NewRegistry()
	for _, ext := range reg.Extensions() {
		c, _ := reg.Get(ext)
		if err := c.Decode([]byte("{}"), nil); !errors.Is(err, codec.ErrNilTarget) {
			t.Fatalf("Expected ErrNilTarget for nil target in %s, got %v", ext, err)
		}

		var s *sampleConfig
		if err := c.Decode([]byte("{}"), s); !errors.Is(err, codec.ErrNilTarget) {
			t.Fatalf("Expected ErrNilTarget for typed nil pointer target in %s, got %v", ext, err)
		}
	}
}

// TestYAMLCodecDistinguishesTrailingDocumentFromMalformedTail pins the two
// reasons a second decode can fail. A stream carrying a second document is
// reported as ErrMultipleDocuments; a stream that is merely malformed after the
// first document is a parse failure, and reporting it as a second document would
// send a caller looking for content that is not there.
func TestYAMLCodecDistinguishesTrailingDocumentFromMalformedTail(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		document  string
		wantMulti bool
	}{
		{"two documents", "version: 1\n---\nversion: 2\n", true},
		{"malformed after the first", "version: 1\n---\n\t\tbad: [\n", false},
		{"one document", "version: 1\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var target sampleConfig

			err := codec.NewYAMLCodec().Decode([]byte(tc.document), &target)

			if got := errors.Is(err, codec.ErrMultipleDocuments); got != tc.wantMulti {
				t.Errorf("ErrMultipleDocuments = %v, want %v (err = %v)", got, tc.wantMulti, err)
			}
		})
	}
}

// Every built-in codec MUST report a document it cannot parse by wrapping
// ErrMalformed, so a caller can tell a broken file from a missing one without
// knowing which format was involved. The parser's own error stays reachable, but
// its type varies by format and is not a contract.
func TestMalformedIsReportedByEveryCodec(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		ext      string
		document string
	}{
		{".toml", "name = [unclosed\n"},
		{".yaml", "name: [unclosed\n"},
		{".json", `{"name": }`},
	} {
		t.Run(tc.ext, func(t *testing.T) {
			t.Parallel()

			registry := codec.NewRegistry()

			instance, ok := registry.Get(tc.ext)
			if !ok {
				t.Fatalf("no codec for %s", tc.ext)
			}

			var target sampleConfig

			err := instance.Decode([]byte(tc.document), &target)
			if err == nil {
				t.Fatalf("%s accepted a malformed document", tc.ext)
			}

			if !errors.Is(err, codec.ErrMalformed) {
				t.Errorf("errors.Is(err, ErrMalformed) = false (err = %v)", err)
			}

			// A malformed document is not a nil target, and the two conditions
			// MUST stay distinguishable.
			if errors.Is(err, codec.ErrNilTarget) {
				t.Errorf("%s reported a nil target for a malformed document", tc.ext)
			}
		})
	}
}

func TestRegistryConcurrentOperations(t *testing.T) {
	t.Parallel()

	reg := codec.NewRegistry()

	var wg sync.WaitGroup

	for range 10 {
		wg.Go(func() {
			for range 50 {
				reg.Restrict(".toml", ".json")
				_ = reg.Extensions()
				_, _ = reg.Get(".toml")
				_, _ = reg.Get(".yaml")
			}
		})

		wg.Go(func() {
			for range 50 {
				reg.Register(".custom", codec.NewTOMLCodec())
				_ = reg.Extensions()
				_, _ = reg.Get(".custom")
			}
		})
	}

	wg.Wait()
}

type strataNamingTestConfig struct {
	ListenPort int    `strata:"port"`
	APIKey     string
}

func TestCrossFormatStrataKeyNaming(t *testing.T) {
	t.Parallel()

	cases := []struct {
		format string
		codec  codec.Codec
		input  string
	}{
		{"json", codec.NewJSONCodec(), `{"port": 9000, "api_key": "secret123"}`},
		{"toml", codec.NewTOMLCodec(), "port = 9000\napi_key = \"secret123\"\n"},
		{"yaml", codec.NewYAMLCodec(), "port: 9000\napi_key: secret123\n"},
	}

	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			t.Parallel()

			var cfg strataNamingTestConfig
			if err := tc.codec.Decode([]byte(tc.input), &cfg); err != nil {
				t.Fatalf("%s decode failed: %v", tc.format, err)
			}
			if cfg.ListenPort != 9000 {
				t.Errorf("%s ListenPort = %d, want 9000", tc.format, cfg.ListenPort)
			}
			if cfg.APIKey != "secret123" {
				t.Errorf("%s APIKey = %q, want \"secret123\"", tc.format, cfg.APIKey)
			}
		})
	}
}
