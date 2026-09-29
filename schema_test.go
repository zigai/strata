package strata_test

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/strata"
)

type chanConfig struct {
	Ch chan int `strata:"ch"`
}

// Schema MUST return an error for a type it cannot describe. The reflector
// panics on such a type, and that panic once escaped a public API.
func TestSchemaReportsUnsupportedTypesInsteadOfPanicking(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		call func() ([]byte, error)
	}{
		{"interface type", func() ([]byte, error) { return strata.Schema[any]() }},
		{"channel field", func() ([]byte, error) { return strata.Schema[chanConfig]() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Schema panicked: %v", r)
				}
			}()

			if _, err := tc.call(); !errors.Is(err, strata.ErrReflectSchema) {
				t.Errorf("err = %v, want it to wrap ErrReflectSchema", err)
			}
		})
	}
}

func TestSchemaGeneration(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[demoConfig](
		strata.WithSchemaID("https://example.com/demo.schema.json"),
		strata.WithSchemaTitle("Demo Configuration"),
	)
	if err != nil {
		t.Fatalf("Schema generation error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal generated schema: %v", err)
	}

	if parsed["$id"] != "https://example.com/demo.schema.json" {
		t.Errorf("$id = %v, want https://example.com/demo.schema.json", parsed["$id"])
	}

	if parsed["title"] != "Demo Configuration" {
		t.Errorf("title = %v, want Demo Configuration", parsed["title"])
	}

	props, ok := parsed["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing in schema")
	}

	if _, ok := props["server_host"]; !ok {
		t.Errorf("server_host property missing from schema")
	}

	if _, ok := props["server_port"]; !ok {
		t.Errorf("server_port property missing from schema")
	}
}

type schemaKeysConfig struct {
	ListenAddr string `strata:"listen"`
	TOMLName   int    `toml:"tname"`
	YAMLName   int    `yaml:"yname"`
	Hidden     int    `strata:"-"`
	Plain      int
	Nested     struct {
		MaxConns int
	}
}

func schemaProperties(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal schema: %v", err)
	}

	props, ok := parsed["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties:\n%s", data)
	}

	return props
}

// The schema names each property by its Go field name in snake_case, as the
// Schema doc says: strata, toml and yaml tags do not rename a property, and a
// field tagged "-" is still listed. Whether the schema should follow the keys
// strata reads instead is open (index To review, "Schema C1 strata keys").
func TestSchemaPropertiesAreSnakeCaseGoNames(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[schemaKeysConfig]()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}

	props := schemaProperties(t, data)

	got := slices.Sorted(maps.Keys(props))
	if want := []string{"hidden", "listen_addr", "nested", "plain", "toml_name", "yaml_name"}; !slices.Equal(got, want) {
		t.Fatalf("properties = %v, want %v", got, want)
	}

	nested, _ := props["nested"].(map[string]any)
	nestedProps, _ := nested["properties"].(map[string]any)

	if _, ok := nestedProps["max_conns"]; !ok || len(nestedProps) != 1 {
		t.Fatalf("nested properties = %v, want only max_conns", nestedProps)
	}
}

// Every key is optional in a config file, so the schema requires none: an
// editor checking a sparse file against it finds no missing properties.
func TestSchemaRequiresNoProperty(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[demoConfig]()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if required, ok := parsed["required"]; ok {
		t.Fatalf("schema requires %v, want no required properties", required)
	}
}

// Without WithSchemaID, $id comes from the type's package path, and two calls
// produce identical bytes.
func TestSchemaDefaultIDAndDeterminism(t *testing.T) {
	t.Parallel()

	first, err := strata.Schema[demoConfig]()
	if err != nil {
		t.Fatal(err)
	}

	second, err := strata.Schema[demoConfig]()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Fatalf("two calls differ:\n%s\n%s", first, second)
	}

	var parsed map[string]any
	if err := json.Unmarshal(first, &parsed); err != nil {
		t.Fatal(err)
	}

	if id, _ := parsed["$id"].(string); !strings.HasPrefix(id, "https://github.com/zigai/strata_test/") {
		t.Fatalf("$id = %q, want it under the package path github.com/zigai/strata_test", id)
	}
}
