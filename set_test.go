package strata_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata"
	"github.com/zigai/strata/codec"
)

func TestSetEndToEnd(t *testing.T) {
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

		if err := strata.Set(p, "server.port", 9090); err != nil {
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

		if err := strata.Set(p, "server.port", 9090); err != nil {
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

		if err := strata.Set(p, "port", 9090); err != nil {
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
	t.Parallel()

	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "config.toml")
	initial := "rate = 1.0\nformat = 'json'\n"

	if err := os.WriteFile(p, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := strata.Set(p, "rate", "1e5"); err != nil {
		t.Fatalf("strata.Set rate error: %v", err)
	}

	if err := strata.Set(p, "format", "t"); err != nil {
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
		{
			"an ambiguous key",
			func() ([]byte, error) {
				return strata.SetBytes(".toml", []byte("port = 1\nPort = 2\n"), "PORT", 3)
			},
			strata.ErrAmbiguousKey,
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
