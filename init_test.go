package strata_test

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

type schemaCollisionConfig struct {
	Schema string `json:"$schema"`
	Port   int    `json:"port"`
	Large  uint64 `json:"large"`
	Nested struct {
		Schema string `json:"$schema"`
	} `json:"nested"`
}

func (config *schemaCollisionConfig) SetDefaults() {
	config.Schema = "https://example.test/original.schema.json"
	config.Port = 8080
	config.Large = 9007199254740993
	config.Nested.Schema = "nested-value"
}

func TestInitReplacesExistingJSONSchemaAndReloads(t *testing.T) {
	t.Parallel()

	for _, schemaURL := range []string{"", "https://example.test/replacement.schema.json"} {
		t.Run("schema="+schemaURL, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.json")
			if err := strata.Init[schemaCollisionConfig](path, strata.WithSchemaURL(schemaURL)); err != nil {
				t.Fatalf("Init: %v", err)
			}

			loaded, err := strata.Load[schemaCollisionConfig](strata.WithDefaults(schemaCollisionConfig{}), strata.WithPath(path), strata.WithFormats("json"), strata.WithStrict())
			if err != nil {
				t.Fatalf("Load generated template: %v", err)
			}

			var want schemaCollisionConfig
			want.SetDefaults()

			if schemaURL != "" {
				want.Schema = schemaURL
			}

			if loaded != want {
				t.Fatalf("loaded = %+v, want %+v", loaded, want)
			}
		})
	}
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

type interfaceSecretConfig struct {
	Value any
}

func (config *interfaceSecretConfig) SetDefaults() {
	config.Value = struct {
		Token string `strata:"token,secret"`
		Keep  string
	}{Token: "token_", Keep: "public-value"}
}

func TestInitOmitsSecretsHeldThroughInterfaces(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := strata.Init[interfaceSecretConfig](path); err != nil {
		t.Fatalf("Init: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var document map[string]map[string]string
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}

	want := map[string]map[string]string{"value": {"keep": "public-value"}}
	if !reflect.DeepEqual(document, want) {
		t.Errorf("template = %v, want %v", document, want)
	}
}

type runtimeSecretConfig struct {
	Value any
}

func (config *runtimeSecretConfig) SetDefaults() {
	config.Value = strata.Secret("token_")
}

type marshaledSecretConfig struct {
	Token string `strata:"token,secret"`
	Keep  string
}

func (config *marshaledSecretConfig) SetDefaults() {
	config.Token = "token_"
	config.Keep = "public-value"
}

func (config *marshaledSecretConfig) MarshalJSON() ([]byte, error) {
	type plain marshaledSecretConfig

	data, err := json.Marshal(plain(*config))
	if err != nil {
		return nil, fmt.Errorf("marshal defaults: %w", err)
	}

	return data, nil
}

type embeddedMarshaledSecretConfig struct {
	marshaledSecretConfig
}

func TestInitOmitsRuntimeAndMarshaledSecrets(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		init func(string) error
		want map[string]string
	}{
		{name: "runtime Secret", init: func(path string) error { return strata.Init[runtimeSecretConfig](path) }, want: map[string]string{}},
		{name: "custom marshaler", init: func(path string) error { return strata.Init[marshaledSecretConfig](path) }, want: map[string]string{"keep": "public-value"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.json")
			if err := test.init(path); err != nil {
				t.Fatal(err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			var document map[string]string
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(document, test.want) {
				t.Errorf("template = %v, want %v", document, test.want)
			}
		})
	}
}

func TestInitRejectsUnexportedMarshaledSecretEmbedding(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "config.json", "{}\n")
	if err := strata.Init[embeddedMarshaledSecretConfig](path, strata.WithOverwrite(true)); err == nil || !strings.Contains(err.Error(), "unexported custom-marshaled embedding") {
		t.Fatalf("Init error = %v; want unsupported embedding", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != "{}\n" {
		t.Fatalf("rejected template changed file to %q", data)
	}
}

type collectionSecretDefaults struct {
	Value any
}

func (config *collectionSecretDefaults) SetDefaults() {
	config.Value = map[string]any{
		"token": strata.Secret("private-value"),
		"keep":  "public",
		"items": []any{strata.Secret("private-value"), "retained"},
	}
}

func TestInitOmitsSecretsInDynamicCollections(t *testing.T) {
	t.Parallel()

	type config struct{ Value any }

	for _, format := range []string{"json", "yaml", "toml"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config."+format)
			if err := strata.Init[collectionSecretDefaults](path); err != nil {
				t.Fatal(err)
			}

			got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats(format))
			if err != nil {
				t.Fatal(err)
			}

			want := config{Value: map[string]any{"keep": "public", "items": []any{"retained"}}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("loaded template = %#v, want %#v", got, want)
			}
		})
	}
}

type interfacePresenceDefaults struct {
	Zero       any `json:",omitzero"`
	False      any `json:",omitzero"`
	Empty      any `json:",omitzero"`
	NilPointer any `json:",omitzero"`
}

func (config *interfacePresenceDefaults) SetDefaults() {
	config.Zero = 0
	config.False = false
	config.Empty = ""
	config.NilPointer = (*int)(nil)
}

func TestInitRetainsInterfaceValues(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := strata.Init[interfacePresenceDefaults](path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{"zero": float64(0), "false": false, "empty": "", "nil_pointer": nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("template = %#v, want %#v", got, want)
	}
}

type byteDefaultsConfig struct {
	Bytes []byte
}

func (config *byteDefaultsConfig) SetDefaults() {
	config.Bytes = []byte{1}
}

func TestInitPreservesByteDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := strata.Init[byteDefaultsConfig](path); err != nil {
		t.Fatal(err)
	}

	type config struct{ Bytes []byte }

	got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats("json"))
	if err != nil {
		t.Fatal(err)
	}

	want := config{Bytes: []byte{1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded defaults = %+v, want %+v", got, want)
	}
}

type DatabaseDefaults struct {
	Port int
}

func (config *DatabaseDefaults) SetDefaults() {
	config.Port = 9000
}

func TestInitPreservesTaggedEmbeddedFields(t *testing.T) {
	t.Parallel()

	type config struct {
		DatabaseDefaults `json:"db"`
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := strata.Init[config](path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var document map[string]map[string]int
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}

	want := map[string]map[string]int{"db": {"port": 9000}}
	if !reflect.DeepEqual(document, want) {
		t.Fatalf("template = %v, want %v", document, want)
	}
}

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

func TestInitTemplatesLoadBackToTheDefaults(t *testing.T) {
	t.Parallel()

	for _, ext := range []string{".toml", ".yaml", ".json"} {
		for _, schemaURL := range []string{"", "https://example.com/schema.json"} {
			t.Run(ext+" schema="+schemaURL, func(t *testing.T) {
				t.Parallel()

				path := filepath.Join(t.TempDir(), "config"+ext)
				if err := strata.Init[demoConfig](path, strata.WithSchemaURL(schemaURL)); err != nil {
					t.Fatalf("Init: %v", err)
				}

				got, err := strata.Load[demoConfig](strata.WithDefaults(demoConfig{}), strata.WithPath(path), strata.WithFormats(ext), strata.WithStrict())
				if err != nil {
					t.Fatalf("Load: %v", err)
				}

				if want := (demoConfig{ServerHost: "127.0.0.1", ServerPort: 8080, Debug: true}); got != want {
					t.Fatalf("got %+v, want %+v", got, want)
				}
			})
		}
	}
}
