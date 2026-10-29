package strata_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/zigai/strata"
	"github.com/zigai/strata/codec"
)

type narrowEditConfig struct {
	Narrow   int8
	Unsigned uint8
}

type listEditConfig struct {
	Tags    []string
	Numbers []int
}

type editLevel string

func (l *editLevel) UnmarshalText(data []byte) error {
	if string(data) != "info" && string(data) != "debug" {
		return errors.New("unknown level")
	}

	*l = editLevel(data)

	return nil
}

type richEditConfig struct {
	Level  editLevel
	Labels map[string]string
	Ratio  float32
}

func TestSetEndToEnd(t *testing.T) {
	type serverConfig struct{ Server struct{ Port int } }

	type portConfig struct{ Port int }

	t.Parallel()

	tmpDir := t.TempDir()

	t.Run("yaml in-place set", func(t *testing.T) {
		t.Parallel()

		p := filepath.Join(tmpDir, "app.yaml")
		initial := `# Header comment
server:
  port: 8080 # default port
`

		if err := os.WriteFile(p, []byte(initial), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		if err := strata.Set[serverConfig](p, "server.port", 9090); err != nil {
			t.Fatalf("strata.Set error: %v", err)
		}

		read, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}

		res := string(read)
		if !strings.Contains(res, "port: 9090") {
			t.Errorf("expected port: 9090, got:\n%s", res)
		}

		if !strings.Contains(res, "# Header comment") {
			t.Errorf("expected header comment preserved, got:\n%s", res)
		}
	})

	t.Run("toml in-place set", func(t *testing.T) {
		t.Parallel()

		p := filepath.Join(tmpDir, "app.toml")
		initial := `# Config
[server]
port = 8080 # listen port
`

		if err := os.WriteFile(p, []byte(initial), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		if err := strata.Set[serverConfig](p, "server.port", 9090); err != nil {
			t.Fatalf("strata.Set error: %v", err)
		}

		read, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}

		res := string(read)
		if !strings.Contains(res, "port = 9090 # listen port") {
			t.Errorf("expected port = 9090 # listen port, got:\n%s", res)
		}

		if !strings.Contains(res, "# Config") {
			t.Errorf("expected # Config preserved, got:\n%s", res)
		}
	})

	t.Run("json in-place set", func(t *testing.T) {
		t.Parallel()

		p := filepath.Join(tmpDir, "app.json")
		initial := "{\n  \"port\": 8080\n}\n"

		if err := os.WriteFile(p, []byte(initial), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		if err := strata.Set[portConfig](p, portKey, 9090); err != nil {
			t.Fatalf("strata.Set error: %v", err)
		}

		read, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile error: %v", err)
		}

		res := string(read)
		if !strings.Contains(res, "\"port\": 9090") {
			t.Errorf("expected \"port\": 9090, got:\n%s", res)
		}
	})
}

func TestSetScientificNotationAndSingleLetter(t *testing.T) {
	type config struct {
		Rate   float64
		Format string
	}

	t.Parallel()

	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "config.toml")
	initial := "rate = 1.0\nformat = 'json'\n"

	if err := os.WriteFile(p, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := strata.Set[config](p, "rate", "1e5"); err != nil {
		t.Fatalf("strata.Set rate error: %v", err)
	}

	if err := strata.Set[config](p, "format", "t"); err != nil {
		t.Fatalf("strata.Set format error: %v", err)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	res := string(data)
	if !strings.Contains(res, "rate = 100000") {
		t.Errorf("expected rate = 100000, got:\n%s", res)
	}

	if !strings.Contains(res, "format = 't'") && !strings.Contains(res, "format = \"t\"") {
		t.Errorf("expected format = 't', got:\n%s", res)
	}
}

type blankValue struct{}

func (blankValue) MarshalYAML() (any, error) { return &yaml.Node{Kind: yaml.ScalarNode}, nil }

func TestSetBytesFailuresAreClassifiable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		call func() ([]byte, error)
		want error
	}{
		{
			"a second document",
			func() ([]byte, error) {
				return strata.SetBytes(".yaml", []byte("a: 1\n---\na: 2\n"), "b", 3)
			},
			strata.ErrMultipleDocuments,
		},
		{
			"a scalar in the way",
			func() ([]byte, error) { return strata.SetBytes(".json", []byte(`{"a": 1}`), "a.b", 2) },
			strata.ErrPathNotMapping,
		},
		{
			"a root that is not a mapping",
			func() ([]byte, error) { return strata.SetBytes(".yaml", []byte("- a\n- b\n"), "a", 1) },
			strata.ErrRootNotMapping,
		},
		{
			"a value that encodes to nothing",
			func() ([]byte, error) { return strata.SetBytes(".yaml", []byte("a: 1\n"), "b", blankValue{}) },
			strata.ErrEmptyEncodedValue,
		},
		{
			"an empty key path",
			func() ([]byte, error) { return strata.SetBytes(".toml", []byte("a = 1\n"), "", 2) },
			strata.ErrEmptyKey,
		},
		{
			"an empty path segment",
			func() ([]byte, error) { return strata.SetBytes(".toml", []byte("a = 1\n"), "a..b", 2) },
			strata.ErrEmptyKeySegment,
		},
		{
			"a duplicate JSON key",
			func() ([]byte, error) { return strata.SetBytes(".json", []byte(`{"a": 1, "a": 2}`), "b", 3) },
			jsontext.ErrDuplicateName,
		},
		{
			"an unsupported format",
			func() ([]byte, error) { return strata.SetBytes(".ini", []byte("a = 1\n"), "a", 2) },
			strata.ErrUnsupportedFormat,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := tc.call()
			if err == nil {
				t.Fatalf("SetBytes accepted %s and returned %q", tc.name, out)
			}

			if !errors.Is(err, tc.want) {
				t.Errorf("errors.Is(err, %v) = false (err = %v)", tc.want, err)
			}
		})
	}
}

// Regression: rewriting once silently discarded trailing YAML documents.
func TestMultiDocumentYAMLIsRefused(t *testing.T) {
	t.Parallel()

	const stream = "small: 1\n---\nsmall: 2\n"

	var target narrowConfig

	if err := codec.NewYAML().Decode([]byte(stream), &target); !errors.Is(err, codec.ErrMultipleDocuments) {
		t.Fatalf("Decode err = %v, want ErrMultipleDocuments", err)
	}

	out, err := strata.SetBytes(".yaml", []byte(stream), "tiny", 3)
	if err == nil {
		t.Fatalf("SetBytes rewrote a multi-document stream as %q, want a refusal", out)
	}

	if !errors.Is(err, strata.ErrMultipleDocuments) {
		t.Errorf("SetBytes err = %v, want it to match strata.ErrMultipleDocuments", err)
	}
}

func TestSetMatchesKeysExactly(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		format string
		input  string
	}{
		{format: ".toml", input: "Port = 1\n"},
		{format: ".yaml", input: "Port: 1\n"},
		{format: ".json", input: "{\"Port\": 1}"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			t.Parallel()

			out, err := strata.SetBytes(tc.format, []byte(tc.input), portKey, 2)
			if err != nil {
				t.Fatalf("SetBytes: %v", err)
			}

			text := string(out)
			if !strings.Contains(text, "Port") || !strings.Contains(text, "1") || !strings.Contains(text, portKey) || !strings.Contains(text, "2") {
				t.Fatalf("SetBytes changed the wrong key or dropped one:\n%s", text)
			}
		})
	}
}

func TestSetBytesAppendedTOMLKeyEndsWithNewline(t *testing.T) {
	for _, input := range []string{"level = 'info'", "level = 'info'\n"} {
		out, err := strata.SetBytes("toml", []byte(input), "zeta", 5)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.HasSuffix(string(out), "zeta = 5\n") {
			t.Fatalf("appended TOML = %q", out)
		}
	}
}

func TestSetChecksKeysAndValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := strata.Init[primitiveConfig](path); err != nil {
		t.Fatal(err)
	}

	if err := strata.Set[primitiveConfig](path, "prot", "9000"); err == nil || !strings.Contains(err.Error(), "did you mean \"port\"") {
		t.Fatalf("unknown key error = %v", err)
	}

	if err := strata.Set[primitiveConfig](path, portKey, "abc"); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("bad value error = %v", err)
	}

	if err := strata.Set[primitiveConfig](path, "timeout", "30s"); err != nil {
		t.Fatal(err)
	}

	if err := strata.Set[primitiveConfig](path, "secret", strata.Secret("private-value")); err != nil {
		t.Fatal(err)
	}

	cfg, err := strata.Load[primitiveConfig](strata.WithPath(path), strata.WithFormats("toml"))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Timeout != 30*time.Second || cfg.Secret.Value() != "private-value" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestSetRejectsNumericOverflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("narrow = 1\nunsigned = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := strata.Set[narrowEditConfig](path, "narrow", 300); err == nil {
		t.Fatal("signed overflow was accepted")
	}

	if err := strata.Set[narrowEditConfig](path, "unsigned", -1); err == nil {
		t.Fatal("negative unsigned value was accepted")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != "narrow = 1\nunsigned = 1\n" {
		t.Fatalf("file changed: %s", data)
	}
}

type numericEditLevel int

func (level *numericEditLevel) UnmarshalText(data []byte) error {
	switch string(data) {
	case "info":
		*level = 1
	case "debug":
		*level = 2
	default:
		return errors.New("unknown level")
	}

	return nil
}

type stringEditLevel string

func (level *stringEditLevel) UnmarshalText(data []byte) error {
	if string(data) != "info" && string(data) != "debug" {
		return errors.New("unknown level")
	}

	*level = stringEditLevel("level:" + string(data))

	return nil
}

func (level stringEditLevel) MarshalText() ([]byte, error) {
	if !strings.HasPrefix(string(level), "level:") {
		return nil, errors.New("undecoded level")
	}

	return []byte(strings.TrimPrefix(string(level), "level:")), nil
}

func TestSetTextDecodedSlicesRoundTrip(t *testing.T) {
	t.Parallel()

	type config struct {
		Delays []time.Duration
		Levels []numericEditLevel
	}

	for format, initial := range map[string]string{
		"toml": "delays = ['1s']\nlevels = ['info']\n",
		"yaml": "delays: [1s]\nlevels: [info]\n",
		"json": `{"delays":["1s"],"levels":["info"]}`,
	} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, "config."+format, initial)
			for _, change := range []struct{ key, value string }{{"delays", "3s, 4s"}, {"levels", "debug, info"}} {
				if err := strata.Set[config](path, change.key, change.value); err != nil {
					t.Fatalf("Set(%s): %v", change.key, err)
				}
			}

			got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats(format))
			if err != nil {
				t.Fatal(err)
			}

			want := config{Delays: []time.Duration{3 * time.Second, 4 * time.Second}, Levels: []numericEditLevel{2, 1}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("config = %+v, want %+v", got, want)
			}

			if err := strata.Set[config](path, "delays", []time.Duration{5 * time.Second, 6 * time.Second}); err != nil {
				t.Fatalf("Set typed durations: %v", err)
			}

			got, err = strata.Load[config](strata.WithPath(path), strata.WithFormats(format))
			if err != nil {
				t.Fatal(err)
			}

			want.Delays = []time.Duration{5 * time.Second, 6 * time.Second}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("typed durations = %+v, want %+v", got, want)
			}

			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			for _, change := range []struct{ key, value string }{{"delays", "3s, invalid"}, {"levels", "debug, invalid"}} {
				if err := strata.Set[config](path, change.key, change.value); err == nil {
					t.Fatalf("Set(%s) accepted invalid item", change.key)
				}

				requireFileContent(t, path, string(before))
			}
		})
	}
}

func TestSetStringTextDecodedSlicesRoundTrip(t *testing.T) {
	t.Parallel()

	type config struct {
		Levels []stringEditLevel
	}

	for _, test := range []struct{ format, initial string }{
		{"json", `{"levels":["info"]}`},
		{"yaml", "levels: [info]\n"},
	} {
		t.Run(test.format, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, "config."+test.format, test.initial)
			if err := strata.Set[config](path, "levels", "debug,info"); err != nil {
				t.Fatal(err)
			}

			got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats(test.format))
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(got.Levels, []stringEditLevel{"level:debug", "level:info"}) {
				t.Fatalf("levels = %v", got.Levels)
			}
		})
	}
}

type csvEditValues []int

func (values csvEditValues) MarshalJSON() ([]byte, error) {
	text, err := values.MarshalYAML()
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(text)
	if err != nil {
		return nil, fmt.Errorf("encode CSV values: %w", err)
	}

	return data, nil
}

func (values *csvEditValues) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("decode CSV values: %w", err)
	}

	parsed, err := parseCSVEditValues(text)
	if err != nil {
		return err
	}

	*values = parsed

	return nil
}

func (values csvEditValues) MarshalYAML() (any, error) {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.Itoa(value)
	}

	return strings.Join(parts, ","), nil
}

func (values *csvEditValues) UnmarshalYAML(node *yaml.Node) error {
	var text string
	if err := node.Decode(&text); err != nil {
		return fmt.Errorf("decode CSV values: %w", err)
	}

	parsed, err := parseCSVEditValues(text)
	if err != nil {
		return err
	}

	*values = parsed

	return nil
}

func parseCSVEditValues(text string) (csvEditValues, error) {
	if text == "" {
		return csvEditValues{}, nil
	}

	var parsed csvEditValues

	for part := range strings.SplitSeq(text, ",") {
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("parse CSV value: %w", err)
		}

		parsed = append(parsed, value)
	}

	return parsed, nil
}

func TestSetPreservesNamedSliceSerialization(t *testing.T) {
	t.Parallel()

	type config struct {
		Values csvEditValues
		Keep   int
	}

	for format, initial := range map[string]string{
		"json": `{"values":"1,2","keep":7}`,
		"yaml": "values: 1,2\nkeep: 7\n",
	} {
		for _, test := range []struct {
			name, raw string
			want      csvEditValues
		}{
			{name: "values", raw: "3,4", want: csvEditValues{3, 4}},
			{name: "empty", raw: "", want: csvEditValues{}},
		} {
			t.Run(format+"/"+test.name, func(t *testing.T) {
				t.Parallel()

				path := writeFile(t, "config."+format, initial)
				if err := strata.Set[config](path, "values", test.raw); err != nil {
					t.Fatal(err)
				}

				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}

				var persisted struct {
					Values string
					Keep   int
				}

				decoder := codec.Codec(codec.NewJSON())
				if format == "yaml" {
					decoder = codec.NewYAML()
				}

				if err := decoder.Decode(data, &persisted); err != nil {
					t.Fatalf("persisted slice lost its CSV representation: %v\n%s", err, data)
				}

				if persisted.Values != test.raw || persisted.Keep != 7 {
					t.Fatalf("persisted = %+v", persisted)
				}

				got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats(format))
				if err != nil {
					t.Fatal(err)
				}

				if !reflect.DeepEqual(got.Values, test.want) || got.Keep != 7 {
					t.Fatalf("config = %+v, want values %v and keep 7", got, test.want)
				}
			})
		}
	}
}

type csvEditLevels []numericEditLevel

func (levels csvEditLevels) MarshalJSON() ([]byte, error) {
	values := make(csvEditValues, len(levels))
	for index, level := range levels {
		values[index] = int(level)
	}

	return values.MarshalJSON()
}

func (levels *csvEditLevels) UnmarshalJSON(data []byte) error {
	var values csvEditValues
	if err := values.UnmarshalJSON(data); err != nil {
		return err
	}

	*levels = make(csvEditLevels, len(values))
	for index, value := range values {
		(*levels)[index] = numericEditLevel(value)
	}

	return nil
}

type csvEditDurations []time.Duration

func (delays csvEditDurations) MarshalJSON() ([]byte, error) {
	values := make(csvEditValues, len(delays))
	for index, delay := range delays {
		values[index] = int(delay / time.Second)
	}

	return values.MarshalJSON()
}

func (delays *csvEditDurations) UnmarshalJSON(data []byte) error {
	var values csvEditValues
	if err := values.UnmarshalJSON(data); err != nil {
		return err
	}

	*delays = make(csvEditDurations, len(values))
	for index, value := range values {
		(*delays)[index] = time.Duration(value) * time.Second
	}

	return nil
}

func TestSetNamedSliceSerializationUsesDecodedElements(t *testing.T) {
	t.Parallel()

	type config struct {
		Levels csvEditLevels
		Delays csvEditDurations
	}

	path := writeFile(t, "config.json", `{"levels":"1","delays":"1"}`)
	for _, change := range []struct{ key, raw string }{{"levels", "debug,info"}, {"delays", "3s,4s"}} {
		if err := strata.Set[config](path, change.key, change.raw); err != nil {
			t.Fatalf("Set(%s): %v", change.key, err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var persisted map[string]string
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}

	wantPersisted := map[string]string{"levels": "2,1", "delays": "3,4"}
	if !reflect.DeepEqual(persisted, wantPersisted) {
		t.Fatalf("persisted = %v, want %v", persisted, wantPersisted)
	}

	got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats("json"))
	if err != nil {
		t.Fatal(err)
	}

	want := config{Levels: csvEditLevels{2, 1}, Delays: csvEditDurations{3 * time.Second, 4 * time.Second}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %+v, want %+v", got, want)
	}

	for _, change := range []struct{ key, raw string }{{"levels", "debug,invalid"}, {"delays", "3s,invalid"}} {
		if err := strata.Set[config](path, change.key, change.raw); err == nil {
			t.Fatalf("Set(%s) accepted invalid item", change.key)
		}

		requireFileContent(t, path, string(data))
	}
}

func TestSetBytesTOMLPreservesLiteralDottedSibling(t *testing.T) {
	t.Parallel()

	updated, err := strata.SetBytes("toml", []byte("[a.\"b.c\"]\nx=1\n[a]\nkeep=2\n"), "a.b", 3)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]map[string]any
	if err := toml.Unmarshal(updated, &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]map[string]any{"a": {"b": int64(3), "b.c": map[string]any{"x": int64(1)}, "keep": int64(2)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("document = %#v, want %#v\n%s", got, want, updated)
	}
}

func TestSetCompositeValuesRoundTrip(t *testing.T) {
	t.Parallel()

	type item struct {
		HTTPPort int `strata:"listen_port"`
		Timeout  time.Duration
	}

	type config struct {
		Keep   int
		Items  []item
		Labels map[string]item
	}

	for name, initial := range map[string]struct{ format, data string }{
		"json":        {"json", `{"keep":7,"items":[],"labels":{}}`},
		"yaml":        {"yaml", "keep: 7\nitems: []\nlabels: {}\n"},
		"toml inline": {"toml", "keep = 7\nitems = []\nlabels = {}\n"},
		"toml tables": {"toml", "keep = 7\n[[items]]\nlisten_port = 1\ntimeout = '1s'\n[labels.old]\nlisten_port = 2\ntimeout = '2s'\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, "config."+initial.format, initial.data)
			for _, change := range []struct {
				key   string
				value any
			}{
				{"items", []item{{HTTPPort: 8080, Timeout: 3 * time.Second}}},
				{"labels", map[string]item{"first": {HTTPPort: 9000, Timeout: 4 * time.Second}}},
			} {
				if err := strata.Set[config](path, change.key, change.value); err != nil {
					t.Fatalf("Set(%s): %v", change.key, err)
				}
			}

			got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats(initial.format))
			if err != nil {
				t.Fatal(err)
			}

			want := config{Keep: 7, Items: []item{{HTTPPort: 8080, Timeout: 3 * time.Second}}, Labels: map[string]item{"first": {HTTPPort: 9000, Timeout: 4 * time.Second}}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("config = %+v, want %+v", got, want)
			}
		})
	}
}

func TestSetRejectsRemovingReferencedYAMLAnchor(t *testing.T) {
	t.Parallel()

	type config struct {
		Base map[string]int
		Copy int
	}

	const initial = "base:\n  x: &value 1\ncopy: *value\n"

	path := writeFile(t, "config.yaml", initial)
	if err := strata.Set[config](path, "base", map[string]int{"y": 2}); !errors.Is(err, strata.ErrMalformed) {
		t.Fatalf("Set = %v, want ErrMalformed", err)
	}

	requireFileContent(t, path, initial)

	got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats("yaml"))
	if err != nil || got.Copy != 1 || !reflect.DeepEqual(got.Base, map[string]int{"x": 1}) {
		t.Fatalf("reload = %+v, %v", got, err)
	}
}

func TestSetParsesLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("tags = []\nnumbers = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := strata.Set[listEditConfig](path, "tags", "a,b"); err != nil {
		t.Fatal(err)
	}

	if err := strata.Set[listEditConfig](path, "numbers", "1, 2"); err != nil {
		t.Fatal(err)
	}

	cfg, err := strata.Load[listEditConfig](strata.WithPath(path), strata.WithFormats("toml"))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(cfg.Tags, []string{"a", "b"}) || !reflect.DeepEqual(cfg.Numbers, []int{1, 2}) {
		t.Fatalf("lists = %+v", cfg)
	}

	if err := strata.Set[listEditConfig](path, "numbers", "1,nope"); err == nil || !strings.Contains(err.Error(), `"nope" is not an integer`) {
		t.Fatalf("list error = %v", err)
	}
}

func TestSetPreservesTaggedEmbeddedFields(t *testing.T) {
	t.Parallel()

	type DB struct{ Port int }

	type config struct {
		DB `json:"db"`
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"db":{"port":9000}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := strata.Set[config](path, "port", 9001); err != nil {
		t.Fatal(err)
	}

	got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats("json"))
	if err != nil {
		t.Fatal(err)
	}

	want := config{Port: 9001}
	if got != want {
		t.Fatalf("updated configuration = %+v, want %+v", got, want)
	}
}

func TestSetPrefersDirectFieldsOverTaggedEmbedding(t *testing.T) {
	t.Parallel()

	type DB struct {
		Port string `json:"port"`
	}

	type config struct {
		DB `json:"db"`

		Port int `json:"port"`
	}

	path := writeFile(t, "config.json", `{"db":{"port":"unchanged"},"port":8080}`)
	if err := strata.Set[config](path, "port", "8081"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var got config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	want := config{DB: DB{Port: "unchanged"}, Port: 8081}
	if got != want {
		t.Fatalf("updated configuration = %+v, want %+v", got, want)
	}
}

func TestSetUsesSymlinkTargetFormat(t *testing.T) {
	t.Parallel()

	type DB struct{ Port int }

	type config struct {
		DB `json:"db"`
	}

	for _, test := range []struct {
		name     string
		target   string
		link     string
		document string
	}{
		{name: "JSON target", target: "actual.json", link: "config.yaml", document: `{"db":{"port":9000}}`},
		{name: "TOML target", target: "actual.toml", link: "config.json", document: "port = 9000\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, test.target, test.document)

			link := filepath.Join(filepath.Dir(path), test.link)
			if err := os.Symlink(path, link); err != nil {
				t.Fatal(err)
			}

			if err := strata.Set[config](link, "port", 9001); err != nil {
				t.Fatal(err)
			}

			got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats(filepath.Ext(path)))
			if err != nil {
				t.Fatal(err)
			}

			if got.Port != 9001 {
				t.Fatalf("updated port = %d, want 9001", got.Port)
			}

			info, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}

			if info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("configuration symlink was replaced")
			}
		})
	}
}

func TestSetRoundsFloat32ToDeclaredPrecision(t *testing.T) {
	t.Parallel()

	type config struct {
		Ratio  float32
		Ratios []float32
	}

	for _, test := range []struct {
		name     string
		format   string
		key      string
		original string
		want     config
	}{
		{name: "scalar", format: "toml", key: "ratio", original: "ratio = 0\n", want: config{Ratio: 0x1.000002p0, Ratios: nil}},
		{name: "slice", format: "json", key: "ratios", original: `{"ratios":[]}`, want: config{Ratio: 0, Ratios: []float32{0x1.000002p0}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config."+test.format)
			if err := os.WriteFile(path, []byte(test.original), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := strata.Set[config](path, test.key, "1.0000000596046448"); err != nil {
				t.Fatal(err)
			}

			got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats(test.format))
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("updated configuration = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestSetDecodesByFieldType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("level = 'info'\nratio = 0\n[labels]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := strata.Set[richEditConfig](path, "level", "bogus"); err == nil || !strings.Contains(err.Error(), "unknown level") {
		t.Fatalf("invalid level error = %v", err)
	}

	if err := strata.Set[richEditConfig](path, "labels.env", "prod"); err != nil {
		t.Fatal(err)
	}

	if err := strata.Set[richEditConfig](path, "ratio", "0.1"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(data), "0.100000001") || !strings.Contains(string(data), "ratio = 0.1") {
		t.Fatalf("float32 edit = %s", data)
	}

	cfg, err := strata.Load[richEditConfig](strata.WithPath(path), strata.WithFormats("toml"))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Level != "info" || cfg.Labels["env"] != "prod" || cfg.Ratio != float32(0.1) {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestSetMapEntryInEachFormat(t *testing.T) {
	for ext, initial := range map[string]string{
		".toml": "[labels]\n",
		".yaml": "labels: {}\n",
		".json": `{"labels":{}}`,
	} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config"+ext)
			if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := strata.Set[richEditConfig](path, "labels.env", "prod"); err != nil {
				t.Fatal(err)
			}

			cfg, err := strata.Load[richEditConfig](strata.WithPath(path), strata.WithFormats(ext))
			if err != nil {
				t.Fatal(err)
			}

			if cfg.Labels["env"] != "prod" {
				t.Fatalf("labels = %v", cfg.Labels)
			}
		})
	}
}

func TestSaveThenSetUsesOneKeySpelling(t *testing.T) {
	t.Parallel()

	for _, ext := range []string{".toml", ".yaml", ".json"} {
		t.Run(ext, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "state"+ext)

			if err := strata.Save(path, typoConfig{Host: "h", Database: typoDatabase{MaxConns: 3}}); err != nil {
				t.Fatalf("Save: %v", err)
			}

			if err := strata.Set[typoConfig](path, "database.max_conns", 9); err != nil {
				t.Fatalf("Set: %v", err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			if strings.Count(string(data), "max_conns") != 1 || strings.Contains(string(data), "MaxConns") {
				t.Fatalf("file names the key more than one way:\n%s", data)
			}

			cfg, err := strata.Load[typoConfig](strata.WithPath(path), strata.WithStrict(), strata.WithFormats(ext))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if cfg.Database.MaxConns != 9 || cfg.Host != "h" {
				t.Fatalf("cfg = %+v, want max_conns 9 and host h", cfg)
			}
		})
	}
}

func TestSetChangesOnlyTheNamedKey(t *testing.T) {
	t.Parallel()

	for ext, initial := range map[string]string{
		".toml": "# settings\nhost = \"db.example\"\nport = 9000\napi_key = \"k\"\n\n[database]\nport = 5432 # primary\nmax_conns = 20\n\n[labels]\nenv = \"prod\"\n",
		".yaml": "# settings\nhost: db.example\nport: 9000\napi_key: k\ndatabase:\n  port: 5432 # primary\n  max_conns: 20\nlabels:\n  env: prod\n",
		".json": `{"host": "db.example", "port": 9000, "api_key": "k", "database": {"port": 5432, "max_conns": 20}, "labels": {"env": "prod"}}`,
	} {
		t.Run(ext, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, "config"+ext, initial)

			if err := strata.Set[typoConfig](path, "database.port", "6543"); err != nil {
				t.Fatalf("Set: %v", err)
			}

			got, err := strata.Load[typoConfig](strata.WithPath(path), strata.WithFormats(ext), strata.WithStrict())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			want := typoConfig{
				Host:     "db.example",
				Port:     9000,
				APIKey:   "k",
				Database: typoDatabase{Port: 6543, MaxConns: 20},
				Labels:   map[string]string{"env": "prod"},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

type rejectEditConfig struct {
	Port    int
	Narrow  int8
	Count   uint
	Debug   bool
	Timeout time.Duration
	Level   editLevel
}

func TestSetRejectionLeavesTheFileUnchanged(t *testing.T) {
	t.Parallel()

	const initial = "port = 8080\nnarrow = 1\ncount = 1\ndebug = false\ntimeout = '1s'\nlevel = 'info'\n"

	for _, tc := range []struct {
		name, key, value string
	}{
		{"out of range for int8", "narrow", "300"},
		{"not an integer", portKey, "abc"},
		{"negative unsigned", "count", "-1"},
		{"not a boolean", "debug", "yes"},
		{"not a duration", "timeout", "5x"},
		{"text decoder rejects", "level", "bogus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, "config.toml", initial)

			err := strata.Set[rejectEditConfig](path, tc.key, tc.value)
			if err == nil {
				t.Fatalf("Set(%s=%q) accepted", tc.key, tc.value)
			}

			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("err = %v, want it to name the key %s", err, tc.key)
			}

			requireFileContent(t, path, initial)
		})
	}

	t.Run("unknown key", func(t *testing.T) {
		t.Parallel()

		path := writeFile(t, "config.toml", initial)

		err := strata.Set[rejectEditConfig](path, "prot", "9000")
		if !errors.Is(err, strata.ErrUnknownKey) {
			t.Fatalf("err = %v, want ErrUnknownKey", err)
		}

		if !strings.Contains(err.Error(), `did you mean "port"`) {
			t.Errorf("err = %v, want a suggestion of port", err)
		}

		requireFileContent(t, path, initial)
	})
}

func requireFileContent(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	if string(data) != want {
		t.Fatalf("file changed:\n%s\nwant:\n%s", data, want)
	}
}

func TestSetKeepsStringFieldsAsStrings(t *testing.T) {
	t.Parallel()

	type stringConfig struct {
		Name string
	}

	for _, ext := range []string{".toml", ".yaml", ".json"} {
		for _, value := range []string{"9090", "true", "1e5"} {
			t.Run(ext+" "+value, func(t *testing.T) {
				t.Parallel()

				path := writeFile(t, "config"+ext, map[string]string{".toml": "name = 'x'\n", ".yaml": "name: x\n", ".json": `{"name": "x"}`}[ext])

				if err := strata.Set[stringConfig](path, "name", value); err != nil {
					t.Fatalf("Set: %v", err)
				}

				if got := decodeGeneric(t, path)["name"]; got != value {
					t.Fatalf("name decodes as %#v (%T), want the string %q", got, got, value)
				}
			})
		}
	}
}

func decodeGeneric(t *testing.T, path string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any

	switch filepath.Ext(path) {
	case ".toml":
		err = toml.Unmarshal(data, &doc)
	case ".yaml":
		err = yaml.Unmarshal(data, &doc)
	case ".json":
		err = json.Unmarshal(data, &doc)
	}

	if err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, data)
	}

	return doc
}

func TestSetBytesInfersValueTypes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value string
		want  any
	}{
		{"9090", int64(9090)},
		{"-3", int64(-3)},
		{"1e5", float64(100000)},
		{"1.5", 1.5},
		{"true", true},
		{"false", false},
		{"t", "t"},
		{"yes", "yes"},
		{"abc", "abc"},
		{"", ""},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()

			out, err := strata.SetBytes(".toml", []byte("other = 1\n"), "v", tc.value)
			if err != nil {
				t.Fatalf("SetBytes: %v", err)
			}

			var doc map[string]any
			if err := toml.Unmarshal(out, &doc); err != nil {
				t.Fatalf("decode: %v\n%s", err, out)
			}

			if !reflect.DeepEqual(doc["v"], tc.want) {
				t.Fatalf("v = %#v (%T), want %#v (%T)", doc["v"], doc["v"], tc.want, tc.want)
			}
		})
	}
}

func TestSetKeepsEveryConcurrentEditToOneFile(t *testing.T) {
	t.Parallel()

	type config struct {
		Counters map[string]int
	}

	path := writeFile(t, "config.toml", "[counters]\n")

	const writers = 8

	errs := make([]error, writers)

	var wg sync.WaitGroup
	for writer := range writers {
		wg.Go(func() {
			errs[writer] = strata.Set[config](path, "counters.writer"+strconv.Itoa(writer), writer)
		})
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats("toml"))
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]int{
		"writer0": 0, "writer1": 1, "writer2": 2, "writer3": 3,
		"writer4": 4, "writer5": 5, "writer6": 6, "writer7": 7,
	}
	if !reflect.DeepEqual(got.Counters, want) {
		t.Fatalf("counters = %v, want every concurrent edit %v", got.Counters, want)
	}
}
