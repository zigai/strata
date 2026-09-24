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

type mergeDatabase struct {
	Host string `strata:"dbHost"`
}

type mergeConfig struct {
	Database mergeDatabase
	Replica  mergeDatabase
}

func TestYAMLMergeKeepsAliasedConfigurationKeys(t *testing.T) {
	for _, tc := range []struct {
		data         string
		wantDatabase string
		wantReplica  string
	}{
		{data: "base: &base\n  dbHost: merged.example\ndatabase:\n  <<: *base\n", wantDatabase: "merged.example"},
		{data: "database: &base\n  dbHost: merged.example\nreplica:\n  <<: *base\n", wantDatabase: "merged.example", wantReplica: "merged.example"},
	} {
		var cfg mergeConfig

		if err := codec.NewYAMLCodec().Decode([]byte(tc.data), &cfg); err != nil {
			t.Fatal(err)
		}

		if cfg.Database.Host != tc.wantDatabase || cfg.Replica.Host != tc.wantReplica {
			t.Fatalf("merge lost database host: %+v", cfg)
		}
	}
}

func TestYAMLMergeRejectsAliasCycle(t *testing.T) {
	data := []byte("database: &db\n  <<: *db\n")

	var cfg mergeConfig
	if err := codec.NewYAMLCodec().Decode(data, &cfg); err == nil {
		t.Fatal("cyclic merge was accepted")
	}
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

// TestDecodersAcceptOnlyConfigurationKeys pins that a field is named by its
// configuration key, spelled exactly. The spellings a decoder would match on its
// own, such as the Go name, are ignored in every format.
func TestDecodersAcceptOnlyConfigurationKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		codec    codec.Codec
		document string
	}{
		{name: "toml", codec: codec.NewTOMLCodec(), document: "http_port = 1\nHTTPPort = 2\nhttpport = 3\n[database]\nmax_conns = 4\nMaxConns = 5\n"},
		{name: "yaml", codec: codec.NewYAMLCodec(), document: "http_port: 1\nHTTPPort: 2\nhttpport: 3\ndatabase:\n  max_conns: 4\n  MaxConns: 5\n"},
		{name: "json", codec: codec.NewJSONCodec(), document: `{"http_port": 1, "HTTPPort": 2, "httpport": 3, "database": {"max_conns": 4, "MaxConns": 5}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var target keyedConfig
			if err := tt.codec.Decode([]byte(tt.document), &target); err != nil {
				t.Fatalf("Decode: %v", err)
			}

			if target.HTTPPort != 1 || target.Database.MaxConns != 4 {
				t.Fatalf("got http_port=%d max_conns=%d, want 1 and 4 from the configuration keys", target.HTTPPort, target.Database.MaxConns)
			}
		})
	}
}
