package codec_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/zigai/strata/codec"
)

type sampleConfig struct {
	Name string   `json:"name" toml:"name" yaml:"name"`
	Port int      `json:"port" toml:"port" yaml:"port"`
	Tags []string `json:"tags" toml:"tags" yaml:"tags"`
}

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

func TestJSONEncodesIndentedSortedJSON(t *testing.T) {
	t.Parallel()

	value := struct {
		Name   string
		Tags   []string
		Limits map[string]int
	}{
		Name:   "my-app",
		Tags:   []string{"web", "api"},
		Limits: map[string]int{"zeta": 1, "alpha": 2},
	}

	encoded, err := codec.NewJSON().Encode(value)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	want := `{
  "name": "my-app",
  "tags": [
    "web",
    "api"
  ],
  "limits": {
    "alpha": 2,
    "zeta": 1
  }
}`
	if string(encoded) != want {
		t.Fatalf("Encode =\n%s\nwant\n%s", encoded, want)
	}
}

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

func TestYAMLDistinguishesTrailingDocumentFromMalformedTail(t *testing.T) {
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

			err := codec.NewYAML().Decode([]byte(tc.document), &target)

			if got := errors.Is(err, codec.ErrMultipleDocuments); got != tc.wantMulti {
				t.Errorf("ErrMultipleDocuments = %v, want %v (err = %v)", got, tc.wantMulti, err)
			}
		})
	}
}

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
				reg.Register(".custom", codec.NewTOML())
				_ = reg.Extensions()
				_, _ = reg.Get(".custom")
			}
		})
	}

	wg.Wait()
}

type strataNamingTestConfig struct {
	ListenPort int `strata:"port"`
	APIKey     string
}

func TestCrossFormatStrataKeyNaming(t *testing.T) {
	t.Parallel()

	cases := []struct {
		format string
		codec  codec.Codec
		input  string
	}{
		{"json", codec.NewJSON(), `{"port": 9000, "api_key": "secret123"}`},
		{"toml", codec.NewTOML(), "port = 9000\napi_key = \"secret123\"\n"},
		{"yaml", codec.NewYAML(), "port: 9000\napi_key: secret123\n"},
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
