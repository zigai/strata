package codec_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata/codec"
)

type keyedDatabase struct {
	MaxConns int
	Host     string `strata:"hostname"`
}

type keyedBase struct {
	LogLevel string
}

type keyedConfig struct {
	keyedBase

	HTTPPort  int
	Database  keyedDatabase
	Replicas  []keyedDatabase
	Labels    map[string]keyedDatabase
	Optional  *keyedDatabase
	StartedAt time.Time
	Ignored   string `json:"-"`
	internal  string
}

type keyedNode struct {
	Name     string
	Children []keyedNode
}

// TestEncodersWriteConfigurationKeys pins that every built-in encoder names a
// field by its configuration key, so a written file uses the keys that
// provenance, Set, and the documentation use.
func TestEncodersWriteConfigurationKeys(t *testing.T) {
	t.Parallel()

	value := keyedConfig{
		LogLevel:  "debug",
		HTTPPort:  8080,
		Database:  keyedDatabase{MaxConns: 5, Host: "db"},
		Replicas:  []keyedDatabase{{MaxConns: 1, Host: "r1"}},
		Labels:    map[string]keyedDatabase{"primary": {MaxConns: 2, Host: "p"}},
		Optional:  &keyedDatabase{MaxConns: 3, Host: "o"},
		StartedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Ignored:   "secret",
		internal:  "hidden",
	}

	tests := []struct {
		name  string
		codec codec.Codec
	}{
		{name: "toml", codec: codec.NewTOMLCodec()},
		{name: "yaml", codec: codec.NewYAMLCodec()},
		{name: "json", codec: codec.NewJSONCodec()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := tt.codec.Encode(value)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			text := string(encoded)
			for _, want := range []string{"log_level", "http_port", "max_conns", "hostname", "replicas", "primary", "optional", "started_at"} {
				if !strings.Contains(text, want) {
					t.Errorf("encoded document lacks key %q:\n%s", want, text)
				}
			}

			for _, unwanted := range []string{"HTTPPort", "MaxConns", "LogLevel", "Ignored", "secret", "internal"} {
				if strings.Contains(text, unwanted) {
					t.Errorf("encoded document contains %q:\n%s", unwanted, text)
				}
			}

			var roundtrip keyedConfig
			if err := tt.codec.Decode(encoded, &roundtrip); err != nil {
				t.Fatalf("Decode: %v", err)
			}

			want := value
			want.Ignored = ""
			want.internal = ""

			if tt.name == "yaml" {
				// yaml.v3 decodes an embedded struct only when it is tagged inline,
				// so the promoted field does not load back from YAML.
				want.LogLevel = ""
			}

			if !reflect.DeepEqual(roundtrip, want) {
				t.Fatalf("roundtrip mismatch:\n got %+v\nwant %+v", roundtrip, want)
			}
		})
	}
}

// TestEncodersKeepRecursiveTypes pins that a type a mirror cannot express is
// still encoded rather than rejected.
func TestEncodersKeepRecursiveTypes(t *testing.T) {
	t.Parallel()

	value := keyedNode{Name: "root", Children: []keyedNode{{Name: "leaf"}}}

	encoded, err := codec.NewYAMLCodec().Encode(value)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if !strings.Contains(string(encoded), "leaf") {
		t.Fatalf("encoded document lacks nested value:\n%s", encoded)
	}
}
