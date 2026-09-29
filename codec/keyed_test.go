package codec_test

import (
	"errors"
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

// keyedLevel has its own text form, which every encoder MUST use.
type keyedLevel int

func (l keyedLevel) MarshalText() ([]byte, error) { return []byte([]string{"info", "debug"}[l]), nil }

func (l *keyedLevel) UnmarshalText(data []byte) error {
	switch string(data) {
	case "info":
		*l = 0
	case "debug":
		*l = 1
	default:
		return errors.New("unknown level")
	}

	return nil
}

type keyedConfig struct {
	keyedBase

	HTTPPort  int
	Timeout   time.Duration
	Level     keyedLevel
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
	if err := codec.NewYAMLCodec().Decode(data, &cfg); !errors.Is(err, codec.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed for a cyclic merge", err)
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
		Timeout:   30 * time.Second,
		Level:     1,
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
			for _, want := range []string{"log_level", "http_port", "max_conns", "hostname", "replicas", "primary", "optional", "started_at", "30s", "debug"} {
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

type formatExcludedConfig struct {
	Name string `toml:"name" yaml:"-"`
	Port int    `strata:"port" json:"-"`
}

func TestFormatTagDashExcludesFieldFromThatFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		codec    codec.Codec
		document string
		want     formatExcludedConfig
	}{
		{name: "yaml", codec: codec.NewYAMLCodec(), document: "name: fromyaml\nport: 2\n", want: formatExcludedConfig{Name: "keep", Port: 2}},
		{name: "json", codec: codec.NewJSONCodec(), document: `{"name": "fromjson", "port": 2}`, want: formatExcludedConfig{Name: "fromjson", Port: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target := formatExcludedConfig{Name: "keep", Port: 1}
			if err := tt.codec.Decode([]byte(tt.document), &target); err != nil {
				t.Fatalf("Decode: %v", err)
			}

			if target != tt.want {
				t.Fatalf("decoded %+v, want %+v", target, tt.want)
			}

			encoded, err := tt.codec.Encode(target)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			excluded := map[string]string{"yaml": "name", "json": "port"}[tt.name]
			if strings.Contains(string(encoded), excluded) {
				t.Errorf("encoded %s holds excluded key %q:\n%s", tt.name, excluded, encoded)
			}
		})
	}
}

type restoreItem struct {
	N      int
	hidden int
}

type restoreInner struct {
	Host string
	Port int
}

type restoreConfig struct {
	Items []restoreItem
	DB    *restoreInner
	Other *restoreInner
	Named map[string]restoreItem
}

// Decoding YAML matches decoding into the target directly: a replaced sequence
// or map entry starts from zero, and a pointer or map is kept and written
// through.
func TestYAMLDecodeKeepsIdentityAndDropsReplacedState(t *testing.T) {
	t.Parallel()

	db := &restoreInner{Host: "db", Port: 1}
	named := map[string]restoreItem{"keep": {N: 1, hidden: 7}, "replace": {N: 2, hidden: 8}}
	target := restoreConfig{Items: []restoreItem{{N: 1, hidden: 7}}, DB: db, Other: db, Named: named}

	document := "items:\n  - n: 9\nnamed:\n  replace:\n    n: 3\n"
	if err := codec.NewYAMLCodec().Decode([]byte(document), &target); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if want := []restoreItem{{N: 9, hidden: 0}}; !reflect.DeepEqual(target.Items, want) {
		t.Errorf("items = %+v, want %+v with no state from the replaced element", target.Items, want)
	}

	if target.DB != db || target.Other != db {
		t.Errorf("pointers not mentioned by the document were replaced")
	}

	wantNamed := map[string]restoreItem{"keep": {N: 1, hidden: 7}, "replace": {N: 3, hidden: 0}}
	if !reflect.DeepEqual(named, wantNamed) || reflect.ValueOf(target.Named).UnsafePointer() != reflect.ValueOf(named).UnsafePointer() {
		t.Errorf("named = %+v (same map: %t), want %+v written into the original map",
			target.Named, reflect.ValueOf(target.Named).UnsafePointer() == reflect.ValueOf(named).UnsafePointer(), wantNamed)
	}

	if err := codec.NewYAMLCodec().Decode([]byte("db:\n  port: 2\n"), &target); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if target.DB != db || db.Port != 2 || db.Host != "db" || target.Other.Port != 2 {
		t.Errorf("db = %+v (same pointer: %t), want port 2 written through the original pointer", target.DB, target.DB == db)
	}
}
