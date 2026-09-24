package strata_test

import (
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"
)

type taggedSecretConfig struct {
	Token   string `strata:",secret"`
	Enabled bool
}

func (c *taggedSecretConfig) SetDefaults() { c.Token = "private" }

type plainDurationConfig struct {
	Timeout time.Duration
	Nested  struct{ Timeout time.Duration }
}

func (c *plainDurationConfig) SetDefaults() {
	c.Timeout = 30 * time.Second
	c.Nested.Timeout = 45 * time.Second
}

func TestInitDirectives(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	schemaURL := "https://example.com/schema.json"

	t.Run("toml taplo directive", func(t *testing.T) {
		t.Parallel()

		p := filepath.Join(tmpDir, "config.toml")

		err := strata.Init[demoConfig](p, strata.WithSchemaURL(schemaURL))
		if err != nil {
			t.Fatalf("Init toml error: %v", err)
		}

		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile error: %v", err)
		}

		content := string(data)
		if !strings.HasPrefix(content, "#:schema https://example.com/schema.json\n") {
			t.Errorf("TOML missing Taplo directive header: %q", content)
		}

		if !strings.Contains(content, "server_host = '127.0.0.1'") && !strings.Contains(content, "server_host = \"127.0.0.1\"") {
			t.Errorf("TOML missing default server_host: %q", content)
		}
	})

	t.Run("yaml language-server directive", func(t *testing.T) {
		t.Parallel()

		p := filepath.Join(tmpDir, "config.yaml")

		err := strata.Init[demoConfig](p, strata.WithSchemaURL(schemaURL))
		if err != nil {
			t.Fatalf("Init yaml error: %v", err)
		}

		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile error: %v", err)
		}

		content := string(data)
		if !strings.HasPrefix(content, "# yaml-language-server: $schema=https://example.com/schema.json\n") {
			t.Errorf("YAML missing language-server directive header: %q", content)
		}

		if !strings.Contains(content, "server_host: 127.0.0.1") {
			t.Errorf("YAML missing default server_host: %q", content)
		}
	})

	t.Run("json schema property", func(t *testing.T) {
		t.Parallel()

		p := filepath.Join(tmpDir, "config.json")

		err := strata.Init[demoConfig](p, strata.WithSchemaURL(schemaURL))
		if err != nil {
			t.Fatalf("Init json error: %v", err)
		}

		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile error: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("Unmarshal JSON error: %v", err)
		}

		if parsed["$schema"] != schemaURL {
			t.Errorf("$schema = %v, want %v", parsed["$schema"], schemaURL)
		}

		if parsed["server_host"] != "127.0.0.1" {
			t.Errorf("server_host = %v, want 127.0.0.1", parsed["server_host"])
		}
	})

	t.Run("fails if file exists without overwrite", func(t *testing.T) {
		t.Parallel()

		p := filepath.Join(tmpDir, "existing.toml")
		if err := os.WriteFile(p, []byte("data = 1\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		err := strata.Init[demoConfig](p)
		if !errors.Is(err, strata.ErrFileExists) {
			t.Fatalf("expected ErrFileExists, got %v", err)
		}

		err = strata.Init[demoConfig](p, strata.WithOverwrite(true))
		if err != nil {
			t.Fatalf("unexpected error with overwrite: %v", err)
		}
	})
}

type concurrentInitConfig struct {
	Port int `json:"port"`
}

var (
	concurrentInitEntered  chan struct{}
	concurrentInitContinue chan struct{}
)

func (c *concurrentInitConfig) SetDefaults() {
	if concurrentInitEntered != nil {
		close(concurrentInitEntered)
		<-concurrentInitContinue
	}

	c.Port = 8080
}

func TestInitNoOverwriteRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	concurrentInitEntered = make(chan struct{})
	concurrentInitContinue = make(chan struct{})
	writerResult := make(chan error, 1)

	go func() {
		<-concurrentInitEntered

		writerResult <- os.WriteFile(path, []byte(`{"port":9000,"owner":"other process"}`), 0o600)

		close(concurrentInitContinue)
	}()

	err := strata.Init[concurrentInitConfig](path)

	if e := <-writerResult; e != nil {
		t.Fatal(e)
	}

	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}

	if !errors.Is(err, strata.ErrFileExists) {
		t.Errorf("Init without overwrite returned %v, want ErrFileExists", err)
	}

	if !strings.Contains(string(data), "other process") {
		t.Errorf("concurrent writer's content was overwritten: %s", data)
	}
}

// Init leaves secret fields out of the template, whether they are marked by the
// Secret type or by the secret tag option.
func TestInitOmitsSecrets(t *testing.T) {
	typed := filepath.Join(t.TempDir(), "config.toml")
	if err := strata.Init[primitiveConfig](typed); err != nil {
		t.Fatal(err)
	}

	initial, err := os.ReadFile(typed)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(initial), "secret") {
		t.Fatalf("secret included in template: %s", initial)
	}

	tagged := filepath.Join(t.TempDir(), "config.yaml")
	if err := strata.Init[taggedSecretConfig](tagged); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(tagged)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(data), "token") {
		t.Fatalf("template includes secret: %s", data)
	}
}

func TestTOMLInitWritesPlainDurationsAsStrings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := strata.Init[plainDurationConfig](path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), "30s") || !strings.Contains(string(data), "45s") || strings.Contains(string(data), "30000000000") {
		t.Fatalf("template = %s", data)
	}

	cfg, err := strata.Load[plainDurationConfig](strata.WithPath(path), strata.WithFormats("toml"))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Timeout != 30*time.Second || cfg.Nested.Timeout != 45*time.Second {
		t.Fatalf("config = %+v", cfg)
	}
}
