package strata_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
