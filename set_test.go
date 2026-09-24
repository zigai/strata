package strata_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

		if err := strata.Set[portConfig](p, "port", 9090); err != nil {
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

func TestSetBytes(t *testing.T) {
	t.Parallel()

	rawTOML := []byte("port = 8080\n")

	updated, err := strata.SetBytes(".toml", rawTOML, "port", 9090)
	if err != nil {
		t.Fatalf("SetBytes error: %v", err)
	}

	if !strings.Contains(string(updated), "port = 9090") {
		t.Errorf("expected port = 9090, got:\n%s", string(updated))
	}
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

// blankValue encodes to an empty document, which leaves a key with nothing to
// hold.
type blankValue struct{}

func (blankValue) MarshalYAML() (any, error) { return &yaml.Node{Kind: yaml.ScalarNode}, nil }

// The conditions SetBytes names MUST be classifiable through a sentinel on this
// package, so a caller never imports a subsystem to branch on one. SetBytes and
// Load report the same value for the same condition, so one check covers a
// document that cannot be read and one that cannot be rewritten. A document the
// parser rejects outright surfaces the parser's own error instead.
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
			strata.ErrNonObjectNavigation,
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
			strata.ErrInvalidEmptyKeyPath,
		},
		{
			"an empty path segment",
			func() ([]byte, error) { return strata.SetBytes(".toml", []byte("a = 1\n"), "a..b", 2) },
			strata.ErrInvalidEmptyPathSegment,
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

// A YAML stream carrying more than one document MUST be refused. It was once
// decoded as its first document and rewritten as only that document, discarding
// the rest of the file.
func TestMultiDocumentYAMLIsRefused(t *testing.T) {
	t.Parallel()

	const stream = "small: 1\n---\nsmall: 2\n"

	var target narrowConfig

	if err := codec.NewYAMLCodec().Decode([]byte(stream), &target); !errors.Is(err, codec.ErrMultipleDocuments) {
		t.Fatalf("Decode err = %v, want ErrMultipleDocuments", err)
	}

	out, err := strata.SetBytes(".yaml", []byte(stream), "tiny", 3)
	if err == nil {
		t.Fatalf("SetBytes rewrote a multi-document stream as %q, want a refusal", out)
	}

	// The same condition reached through either API MUST carry one identity.
	if !errors.Is(err, strata.ErrMultipleDocuments) {
		t.Errorf("SetBytes err = %v, want it to match strata.ErrMultipleDocuments", err)
	}
}

// A key is matched exactly. A key differing only in case is a different key,
// so it is left alone and the requested key is added beside it.
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

			out, err := strata.SetBytes(tc.format, []byte(tc.input), "port", 2)
			if err != nil {
				t.Fatalf("SetBytes: %v", err)
			}

			text := string(out)
			if !strings.Contains(text, "Port") || !strings.Contains(text, "1") || !strings.Contains(text, "port") || !strings.Contains(text, "2") {
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

// Set rejects unknown keys and badly typed values, and writes valid values in a
// form that loads back.
func TestSetChecksKeysAndValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := strata.Init[primitiveConfig](path); err != nil {
		t.Fatal(err)
	}

	if err := strata.Set[primitiveConfig](path, "prot", "9000"); err == nil || !strings.Contains(err.Error(), "did you mean \"port\"") {
		t.Fatalf("unknown key error = %v", err)
	}

	if err := strata.Set[primitiveConfig](path, "port", "abc"); err == nil || !strings.Contains(err.Error(), path) {
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
