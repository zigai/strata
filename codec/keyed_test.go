package codec_test

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata/codec"

	"gopkg.in/yaml.v3"
)

type keyedDatabase struct {
	MaxConns int
	Host     string `strata:"hostname"`
}

type keyedBase struct {
	LogLevel string
}

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
	HTTPPort int
	Timeout  time.Duration
	Next     *keyedNode
	Children []keyedNode `json:",omitempty" yaml:",omitempty" toml:",omitempty"`
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
		{data: "base: &base\n  dbHost: merged.example\ndatabase: *base\n", wantDatabase: "merged.example"},
		{data: "database: &base\n  dbHost: merged.example\nreplica: *base\n", wantDatabase: "merged.example", wantReplica: "merged.example"},
		{data: "database: &base\n  dbHost: merged.example\nreplica:\n  <<: *base\n", wantDatabase: "merged.example", wantReplica: "merged.example"},
	} {
		var cfg mergeConfig

		if err := codec.NewYAML().Decode([]byte(tc.data), &cfg); err != nil {
			t.Fatal(err)
		}

		if cfg.Database.Host != tc.wantDatabase || cfg.Replica.Host != tc.wantReplica {
			t.Fatalf("merge lost database host: %+v", cfg)
		}
	}
}

func TestYAMLRecursiveConfigurationResolvesAliases(t *testing.T) {
	t.Parallel()

	var got struct {
		Database *keyedNode
		Replica  *keyedNode
	}

	data := "base: &base\n  http_port: 9000\n  timeout: 1s\n  next: &next {http_port: 7000, timeout: 2s}\ndatabase: *base\nreplica: {<<: *base}\n"
	if err := codec.NewYAML().Decode([]byte(data), &got); err != nil {
		t.Fatal(err)
	}

	want := &keyedNode{HTTPPort: 9000, Timeout: time.Second, Next: &keyedNode{HTTPPort: 7000, Timeout: 2 * time.Second}}
	if !reflect.DeepEqual(got.Database, want) || !reflect.DeepEqual(got.Replica, want) {
		t.Fatalf("aliases = %+v, want both nodes %+v", got, want)
	}
}

func TestYAMLMergeRejectsAliasCycle(t *testing.T) {
	data := []byte("database: &db\n  <<: *db\n")

	var cfg mergeConfig
	if err := codec.NewYAML().Decode(data, &cfg); !errors.Is(err, codec.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed for a cyclic merge", err)
	}
}

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
		{name: "toml", codec: codec.NewTOML()},
		{name: "yaml", codec: codec.NewYAML()},
		{name: "json", codec: codec.NewJSON()},
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

func TestEncodersKeepRecursiveTypes(t *testing.T) {
	t.Parallel()

	value := keyedNode{HTTPPort: 8080, Timeout: 2 * time.Second, Next: &keyedNode{HTTPPort: 9000, Timeout: time.Second}, Children: []keyedNode{{HTTPPort: 7000, Timeout: 3 * time.Second}}}

	for _, tc := range []struct {
		name  string
		codec codec.Codec
	}{
		{name: "json", codec: codec.NewJSON()},
		{name: "toml", codec: codec.NewTOML()},
		{name: "yaml", codec: codec.NewYAML()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := tc.codec.Encode(value)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			var got keyedNode
			if err := tc.codec.Decode(encoded, &got); err != nil {
				t.Fatalf("Decode: %v", err)
			}

			if !reflect.DeepEqual(got, value) {
				t.Fatalf("roundtrip = %+v, want %+v; document:\n%s", got, value, encoded)
			}
		})
	}
}

type recursiveEmbeddedNode struct {
	*recursiveEmbeddedNode

	HTTPPort int
}

func TestEncodersHandleRecursiveAnonymousPointers(t *testing.T) {
	if format := os.Getenv("STRATA_TEST_RECURSIVE_EMBED"); format != "" {
		debug.SetMaxStack(128 << 10)

		encoder := map[string]codec.Codec{"json": codec.NewJSON(), "toml": codec.NewTOML(), "yaml": codec.NewYAML()}[format]
		for _, value := range []recursiveEmbeddedNode{
			{HTTPPort: 8080},
			{recursiveEmbeddedNode: &recursiveEmbeddedNode{HTTPPort: 9000}, HTTPPort: 8080},
		} {
			data, err := encoder.Encode(value)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			var got map[string]int
			if err := encoder.Decode(data, &got); err != nil {
				t.Fatalf("Decode: %v", err)
			}

			if !reflect.DeepEqual(got, map[string]int{"http_port": 8080}) {
				t.Fatalf("document = %s, want only http_port=8080", data)
			}
		}

		return
	}

	for _, format := range []string{"json", "toml", "yaml"} {
		t.Run(format, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestEncodersHandleRecursiveAnonymousPointers$")

			command.Env = append(os.Environ(), "STRATA_TEST_RECURSIVE_EMBED="+format)

			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("recursive embedding subprocess: %v\n%s", err, output)
			}
		})
	}
}

type zeroKeyedNode struct {
	Value    string
	HTTPPort int
	Next     *zeroKeyedNode `json:",omitzero" yaml:",omitempty"`
}

func (node *zeroKeyedNode) IsZero() bool { return node.Value == "" }

type zeroKeyedValue struct {
	HTTPPort int
	Next     *zeroKeyedValue
}

func (node zeroKeyedValue) IsZero() bool { return node.HTTPPort < 0 }

func TestRecursiveEncodersHonorCustomIsZero(t *testing.T) {
	t.Parallel()

	omitted := &zeroKeyedNode{HTTPPort: 9000}
	omitted.Next = omitted

	for _, encoder := range []struct {
		name  string
		codec codec.Codec
	}{
		{name: "json", codec: codec.NewJSON()},
		{name: "yaml", codec: codec.NewYAML()},
	} {
		t.Run(encoder.name, func(t *testing.T) {
			for _, test := range []struct {
				name  string
				value any
				want  map[string]any
			}{
				{
					name:  "omit custom zero pointer",
					value: zeroKeyedNode{Value: "root", HTTPPort: 8080, Next: &zeroKeyedNode{HTTPPort: 9000}},
					want:  map[string]any{"value": "root", "http_port": 8080},
				},
				{
					name:  "omit cyclic custom zero pointer",
					value: zeroKeyedNode{Value: "root", HTTPPort: 8080, Next: omitted},
					want:  map[string]any{"value": "root", "http_port": 8080},
				},
				{
					name:  "keep custom nonzero pointer",
					value: zeroKeyedNode{Value: "root", HTTPPort: 8080, Next: &zeroKeyedNode{Value: "child", HTTPPort: 9000}},
					want:  map[string]any{"value": "root", "http_port": 8080, "next": map[string]any{"value": "child", "http_port": 9000}},
				},
				{
					name: "keep custom nonzero value",
					value: struct {
						Node zeroKeyedValue `json:",omitzero" yaml:",omitempty"`
					}{},
					want: map[string]any{"node": map[string]any{"http_port": 0, "next": nil}},
				},
				{
					name: "omit custom zero value",
					value: struct {
						Node zeroKeyedValue `json:",omitzero" yaml:",omitempty"`
					}{Node: zeroKeyedValue{HTTPPort: -1}},
					want: map[string]any{},
				},
			} {
				t.Run(test.name, func(t *testing.T) {
					data, err := encoder.codec.Encode(test.value)
					if err != nil {
						t.Fatalf("Encode: %v", err)
					}

					var got map[string]any
					if err := yaml.Unmarshal(data, &got); err != nil {
						t.Fatalf("read encoded document: %v", err)
					}

					if !reflect.DeepEqual(got, test.want) {
						t.Fatalf("document = %s; decoded = %#v, want %#v", data, got, test.want)
					}
				})
			}
		})
	}
}

func TestYAMLEncoderSupportsInlineMaps(t *testing.T) {
	t.Parallel()

	value := struct {
		Name   string
		Extras map[string]string `yaml:",inline"`
	}{Name: "public", Extras: map[string]string{"extra": "\nvalue"}}

	data, err := codec.NewYAML().Encode(value)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]string
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"name": "public", "extra": "\nvalue"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("document = %#v, want %#v", got, want)
	}
}

func TestYAMLEncoderRejectsMultilineInlineMapKeys(t *testing.T) {
	t.Parallel()

	value := struct {
		Extras map[string]int `yaml:",inline"`
	}{Extras: map[string]int{"\nextra": 1}}

	data, err := codec.NewYAML().Encode(value)
	if err == nil || !strings.Contains(err.Error(), "inline YAML map keys cannot contain newlines") || data != nil {
		t.Fatalf("Encode = %q, %v; want no document and unsupported inline key", data, err)
	}
}

func TestYAMLEncoderRetainsInterfaceValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value any
		want  map[string]any
	}{
		{name: "zero", value: 0, want: map[string]any{"value": 0}},
		{name: "false", value: false, want: map[string]any{"value": false}},
		{name: "empty string", value: "", want: map[string]any{"value": ""}},
		{name: "empty slice", value: []string{}, want: map[string]any{"value": []any{}}},
		{name: "empty map", value: map[string]int{}, want: map[string]any{"value": map[string]any{}}},
		{name: "nil pointer", value: (*int)(nil), want: map[string]any{"value": nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := struct {
				Value any `yaml:",omitempty"`
			}{Value: test.value}

			data, err := codec.NewYAML().Encode(value)
			if err != nil {
				t.Fatal(err)
			}

			var got map[string]any
			if err := yaml.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("document = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestYAMLEncoderOmitsCustomZeroInterfaceValues(t *testing.T) {
	t.Parallel()

	for _, value := range []any{zeroKeyedValue{HTTPPort: -1}, (*zeroKeyedNode)(nil)} {
		data, err := codec.NewYAML().Encode(struct {
			Node any `yaml:",omitempty"`
		}{Node: value})
		if err != nil {
			t.Fatal(err)
		}

		var got map[string]any
		if err := yaml.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(got, map[string]any{}) {
			t.Fatalf("document = %#v; want no custom zero value", got)
		}
	}
}

func TestOmitSecretsRetainsCustomZeroInJSONInterface(t *testing.T) {
	t.Parallel()

	value := struct {
		Node any `json:",omitzero"`
	}{Node: zeroKeyedValue{HTTPPort: -1}}

	template, err := codec.OmitSecrets(value, "json")
	if err != nil {
		t.Fatal(err)
	}

	data, err := codec.NewJSON().Encode(template)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{"node": map[string]any{"http_port": -1, "next": nil}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("document = %#v, want %#v", got, want)
	}
}

func TestEncodersRejectCyclicRecursiveValues(t *testing.T) {
	t.Parallel()

	value := &keyedNode{HTTPPort: 8080}
	value.Next = value

	for _, tc := range []struct {
		name  string
		codec codec.Codec
	}{
		{name: "json", codec: codec.NewJSON()},
		{name: "toml", codec: codec.NewTOML()},
		{name: "yaml", codec: codec.NewYAML()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.codec.Encode(value)
			if err == nil || data != nil {
				t.Fatalf("Encode = %q, %v; want no document and a cycle error", data, err)
			}
		})
	}
}

func TestDecodersAcceptOnlyConfigurationKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		codec    codec.Codec
		document string
	}{
		{name: "toml", codec: codec.NewTOML(), document: "http_port = 1\nHTTPPort = 2\nhttpport = 3\n[database]\nmax_conns = 4\nMaxConns = 5\n"},
		{name: "yaml", codec: codec.NewYAML(), document: "http_port: 1\nHTTPPort: 2\nhttpport: 3\ndatabase:\n  max_conns: 4\n  MaxConns: 5\n"},
		{name: "json", codec: codec.NewJSON(), document: `{"http_port": 1, "HTTPPort": 2, "httpport": 3, "database": {"max_conns": 4, "MaxConns": 5}}`},
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
		{name: "yaml", codec: codec.NewYAML(), document: "name: fromyaml\nport: 2\n", want: formatExcludedConfig{Name: "keep", Port: 2}},
		{name: "json", codec: codec.NewJSON(), document: `{"name": "fromjson", "port": 2}`, want: formatExcludedConfig{Name: "fromjson", Port: 1}},
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

type textDecodedStruct struct {
	Address string
}

func (value *textDecodedStruct) UnmarshalText(data []byte) error {
	value.Address = "text:" + string(data)
	return nil
}

type yamlDecodedStruct struct {
	Address string
}

func (value *yamlDecodedStruct) UnmarshalYAML(node *yaml.Node) error {
	value.Address = "yaml:" + node.Value
	return nil
}

func TestYAMLKeepsStructDecoderMethods(t *testing.T) {
	t.Parallel()

	var got struct {
		Text    textDecodedStruct
		YAML    yamlDecodedStruct
		Pointer *textDecodedStruct
		Items   []textDecodedStruct
		Named   map[string]yamlDecodedStruct
	}
	if err := codec.NewYAML().Decode([]byte("text: one\nyaml: two\npointer: three\nitems: [four]\nnamed: {entry: five}\n"), &got); err != nil {
		t.Fatal(err)
	}

	if got.Text.Address != "text:one" || got.YAML.Address != "yaml:two" || got.Pointer == nil || got.Pointer.Address != "text:three" || !reflect.DeepEqual(got.Items, []textDecodedStruct{{Address: "text:four"}}) || !reflect.DeepEqual(got.Named, map[string]yamlDecodedStruct{"entry": {Address: "yaml:five"}}) {
		t.Fatalf("custom decoders were not honored: %+v", got)
	}
}

func TestYAMLIgnoresCompoundKeysInUnknownSubtrees(t *testing.T) {
	t.Parallel()

	for _, document := range []string{
		"http_port: 80\nignored: {? [x, y]: value}\n",
		"http_port: 80\nignored: &ignored {? [x, y]: value}\nother: *ignored\n",
	} {
		t.Run(document, func(t *testing.T) {
			var mirrored struct{ HTTPPort int }
			if err := codec.NewYAML().Decode([]byte(document), &mirrored); err != nil {
				t.Fatalf("Decode mirrored struct: %v", err)
			}

			if mirrored.HTTPPort != 80 {
				t.Fatalf("http_port = %d, want 80", mirrored.HTTPPort)
			}

			var recursive keyedNode
			if err := codec.NewYAML().Decode([]byte(document), &recursive); err != nil {
				t.Fatalf("Decode recursive struct: %v", err)
			}

			if recursive.HTTPPort != 80 {
				t.Fatalf("recursive http_port = %d, want 80", recursive.HTTPPort)
			}
		})
	}
}

type compoundYAMLMapping struct {
	Node yaml.Node
}

func (value *compoundYAMLMapping) UnmarshalYAML(node *yaml.Node) error {
	value.Node = *node
	return nil
}

func TestYAMLPassesCompoundKeysToCustomDecoders(t *testing.T) {
	t.Parallel()

	root := new(compoundYAMLMapping)
	nested := new(struct{ Custom compoundYAMLMapping })

	for _, test := range []struct {
		name     string
		document string
		target   any
		node     *yaml.Node
	}{
		{name: "root", document: "? [x, y]\n: value\n", target: root, node: &root.Node},
		{name: "field alias", document: "ignored: &raw {? [x, y]: value}\ncustom: *raw\n", target: nested, node: &nested.Custom.Node},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := codec.NewYAML().Decode([]byte(test.document), test.target); err != nil {
				t.Fatalf("Decode: %v", err)
			}

			if test.node.Kind != yaml.MappingNode || len(test.node.Content) != 2 {
				t.Fatalf("custom decoder node = %+v, want one mapping entry", test.node)
			}

			key, value := test.node.Content[0], test.node.Content[1]
			if key.Kind != yaml.SequenceNode || len(key.Content) != 2 || key.Content[0].Value != "x" || key.Content[1].Value != "y" || value.Value != "value" {
				t.Fatalf("custom decoder mapping = %+v: %+v, want [x, y]: value", key, value)
			}
		})
	}
}

func TestYAMLRejectsCompoundMergeKeysWithoutPanic(t *testing.T) {
	t.Parallel()

	for _, data := range []string{
		"database: {<<, [x]}\n",
		"base: &base {<<, [x]}\ndatabase: *base\n",
		"database: {<<: {dbHost: example}, ? [x]: value}\n",
	} {
		t.Run(data, func(t *testing.T) {
			var got mergeConfig
			if err := codec.NewYAML().Decode([]byte(data), &got); !errors.Is(err, codec.ErrMalformed) {
				t.Fatalf("Decode error = %v, want ErrMalformed", err)
			}
		})
	}
}

func FuzzYAMLDecode(f *testing.F) {
	for _, data := range []string{"", "database: {dbHost: example}", "database: {<<, [x]}", "base: &base {dbHost: example}\ndatabase: *base", "database: &base {<<: *base}"} {
		f.Add(data)
	}

	f.Fuzz(func(t *testing.T, data string) {
		var got mergeConfig

		_ = codec.NewYAML().Decode([]byte(data), &got)
	})
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

func TestYAMLDecodeKeepsIdentityAndMergesMapEntries(t *testing.T) {
	t.Parallel()

	db := &restoreInner{Host: "db", Port: 1}
	named := map[string]restoreItem{"keep": {N: 1, hidden: 7}, "replace": {N: 2, hidden: 8}}
	target := restoreConfig{Items: []restoreItem{{N: 1, hidden: 7}}, DB: db, Other: db, Named: named}

	document := "items:\n  - n: 9\nnamed:\n  replace:\n    n: 3\n"
	if err := codec.NewYAML().Decode([]byte(document), &target); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if want := []restoreItem{{N: 9, hidden: 0}}; !reflect.DeepEqual(target.Items, want) {
		t.Errorf("items = %+v, want %+v with no state from the replaced element", target.Items, want)
	}

	if target.DB != db || target.Other != db {
		t.Errorf("pointers not mentioned by the document were replaced")
	}

	wantNamed := map[string]restoreItem{"keep": {N: 1, hidden: 7}, "replace": {N: 3, hidden: 8}}
	if !reflect.DeepEqual(named, wantNamed) || reflect.ValueOf(target.Named).UnsafePointer() != reflect.ValueOf(named).UnsafePointer() {
		t.Errorf("named = %+v (same map: %t), want %+v written into the original map",
			target.Named, reflect.ValueOf(target.Named).UnsafePointer() == reflect.ValueOf(named).UnsafePointer(), wantNamed)
	}

	if err := codec.NewYAML().Decode([]byte("db:\n  port: 2\n"), &target); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if target.DB != db || db.Port != 2 || db.Host != "db" || target.Other.Port != 2 {
		t.Errorf("db = %+v (same pointer: %t), want port 2 written through the original pointer", target.DB, target.DB == db)
	}
}

type catchAllConfig struct {
	Name  string            `yaml:"name"`
	Extra map[string]string `yaml:",inline"`
}

func TestYAMLInlineMapsMergeUndeclaredKeysAcrossLayers(t *testing.T) {
	t.Parallel()

	var got catchAllConfig
	if err := codec.NewYAML().Decode([]byte("name: base\ncustom: kept\n"), &got); err != nil {
		t.Fatal(err)
	}

	if err := codec.NewYAML().Decode([]byte("other: added\nname: top\n"), &got); err != nil {
		t.Fatal(err)
	}

	want := catchAllConfig{Name: "top", Extra: map[string]string{"custom": "kept", "other": "added"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded = %#v, want %#v", got, want)
	}
}

func TestYAMLInlineMapsRoundTrip(t *testing.T) {
	t.Parallel()

	value := catchAllConfig{Name: "example", Extra: map[string]string{"custom": "kept"}}

	data, err := codec.NewYAML().Encode(value)
	if err != nil {
		t.Fatal(err)
	}

	var got catchAllConfig
	if err := codec.NewYAML().Decode(data, &got); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, value) {
		t.Errorf("round trip of %s = %#v, want %#v", data, got, value)
	}
}

func TestYAMLInlineMapErrorsNameTheEntryKey(t *testing.T) {
	t.Parallel()

	var got struct {
		Extra map[string]int `yaml:",inline"`
	}

	err := codec.NewYAML().Decode([]byte("count: many\n"), &got)

	keyErr, ok := errors.AsType[*codec.KeyError](err)
	if !ok || keyErr.Key != "count" || keyErr.Line != 1 {
		t.Fatalf("Decode error = %v, want a KeyError for count on line 1", err)
	}
}

type jsonOptionConfig struct {
	Count  int    `json:",string"`
	Region string `json:",case:ignore"`
}

func TestJSONStringOptionRoundTrips(t *testing.T) {
	t.Parallel()

	value := jsonOptionConfig{Count: 7, Region: "north"}

	data, err := codec.NewJSON().Encode(value)
	if err != nil {
		t.Fatal(err)
	}

	var got jsonOptionConfig
	if err := codec.NewJSON().Decode(data, &got); err != nil {
		t.Fatalf("Decode(%s) error = %v", data, err)
	}

	if !reflect.DeepEqual(got, value) {
		t.Fatalf("Decode(%s) = %+v, want %+v", data, got, value)
	}
}

func TestJSONCaseIgnoreOptionMatchesOtherSpellings(t *testing.T) {
	t.Parallel()

	var got jsonOptionConfig
	if err := codec.NewJSON().Decode([]byte(`{"REGION":"north","Count":"3"}`), &got); err != nil {
		t.Fatal(err)
	}

	if got.Region != "north" || got.Count != 0 {
		t.Fatalf("decoded = %+v, want region north and count left unset", got)
	}
}

func TestJSONFieldOptionErrorsNameTheKey(t *testing.T) {
	t.Parallel()

	var got jsonOptionConfig

	err := codec.NewJSON().Decode([]byte("{\n\"count\": \"seven\"}"), &got)

	keyErr, ok := errors.AsType[*codec.KeyError](err)
	if !ok || keyErr.Key != "count" || keyErr.Line != 2 {
		t.Fatalf("Decode error = %v, want a KeyError for count on line 2", err)
	}

	if strings.Contains(err.Error(), "struct {") {
		t.Errorf("Decode error = %q, want no mention of an internal holder type", err)
	}
}

type sizer interface{ Size() int }

type sizedBox struct {
	Width  int `json:"width" toml:"width" yaml:"width"`
	Height int `json:"height" toml:"height" yaml:"height"`
}

func (b *sizedBox) Size() int { return b.Width * b.Height }

type sizedLength int

func (l sizedLength) Size() int { return int(l) }

type interfaceConfig struct {
	Value sizer `json:"value" toml:"value" yaml:"value"`
}

func interfaceCodecs() []struct {
	name    string
	codec   codec.Codec
	mapping string
	number  string
} {
	return []struct {
		name    string
		codec   codec.Codec
		mapping string
		number  string
	}{
		{"yaml", codec.NewYAML(), "value:\n  width: 3\n", "value: 4\n"},
		{"json", codec.NewJSON(), `{"value":{"width":3}}`, `{"value":4}`},
		{"toml", codec.NewTOML(), "[value]\nwidth = 3\n", "value = 4\n"},
	}
}

func TestDecodersRejectNilInterfacesWithMethods(t *testing.T) {
	t.Parallel()

	for _, tt := range interfaceCodecs() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got interfaceConfig

			err := tt.codec.Decode([]byte(tt.mapping), &got)

			keyErr, ok := errors.AsType[*codec.KeyError](err)
			if !ok || keyErr.Key != "value" || !errors.Is(err, codec.ErrMalformed) {
				t.Fatalf("Decode error = %v, want a malformed KeyError for value", err)
			}

			if got.Value != nil {
				t.Errorf("Value = %#v, want nil", got.Value)
			}
		})
	}
}

func TestDecodersFillTheConfiguredInterfaceImplementation(t *testing.T) {
	t.Parallel()

	for _, tt := range interfaceCodecs() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			box := &sizedBox{Width: 1, Height: 2}
			got := interfaceConfig{Value: box}

			if err := tt.codec.Decode([]byte(tt.mapping), &got); err != nil {
				t.Fatal(err)
			}

			if got.Value != box || *box != (sizedBox{Width: 3, Height: 2}) {
				t.Fatalf("Value = %#v, want the configured box with width 3 and height 2", got.Value)
			}

			length := interfaceConfig{Value: sizedLength(1)}
			if err := tt.codec.Decode([]byte(tt.number), &length); err != nil {
				t.Fatal(err)
			}

			if length.Value != sizedLength(4) {
				t.Fatalf("Value = %#v, want sizedLength(4)", length.Value)
			}
		})
	}
}

func TestDecodersRejectShapesTheInterfaceImplementationCannotHold(t *testing.T) {
	t.Parallel()

	for _, tt := range interfaceCodecs() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := interfaceConfig{Value: sizedLength(1)}

			err := tt.codec.Decode([]byte(tt.mapping), &got)

			if keyErr, ok := errors.AsType[*codec.KeyError](err); !ok || keyErr.Key != "value" {
				t.Fatalf("Decode error = %v, want a KeyError for value", err)
			}
		})
	}
}

type inlineLevel struct {
	Level int `yaml:"level"`
}

type inlineServer struct {
	Host string      `yaml:"host"`
	Port int         `yaml:"port"`
	Deep inlineLevel `yaml:"deep"`
}

type inlineMode struct {
	Mode string `yaml:"mode"`
}

type inlineStructConfig struct {
	Name   string       `yaml:"name"`
	Server inlineServer `yaml:",inline"`
	Mode   *inlineMode  `yaml:",inline"`
}

func TestYAMLInlineStructsRoundTrip(t *testing.T) {
	t.Parallel()

	value := inlineStructConfig{
		Name:   "example",
		Server: inlineServer{Host: "localhost", Port: 1, Deep: inlineLevel{Level: 2}},
		Mode:   &inlineMode{Mode: "fast"},
	}

	data, err := codec.NewYAML().Encode(value)
	if err != nil {
		t.Fatal(err)
	}

	var got inlineStructConfig
	if err := codec.NewYAML().Decode(data, &got); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, value) {
		t.Fatalf("round trip of %s = %+v, want %+v", data, got, value)
	}
}

func TestYAMLInlineStructsReadTopLevelKeysAndMergeLayers(t *testing.T) {
	t.Parallel()

	var got inlineStructConfig
	if err := codec.NewYAML().Decode([]byte("name: base\nhost: one\nport: 1\ndeep:\n  level: 3\n"), &got); err != nil {
		t.Fatal(err)
	}

	if err := codec.NewYAML().Decode([]byte("port: 2\nserver:\n  host: ignored\n"), &got); err != nil {
		t.Fatal(err)
	}

	want := inlineStructConfig{Name: "base", Server: inlineServer{Host: "one", Port: 2, Deep: inlineLevel{Level: 3}}, Mode: nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded = %+v, want %+v with the inline pointer left nil", got, want)
	}
}

func TestYAMLInlineStructErrorsNameTheTopLevelKey(t *testing.T) {
	t.Parallel()

	var got inlineStructConfig

	err := codec.NewYAML().Decode([]byte("name: x\nport: many\n"), &got)

	keyErr, ok := errors.AsType[*codec.KeyError](err)
	if !ok || keyErr.Key != "port" || keyErr.Line != 2 {
		t.Fatalf("Decode error = %v, want a KeyError for port on line 2", err)
	}
}
