package strata_test

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"maps"
	"net/http"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/strata"
)

type DescribedSchemaConfig struct {
	// ListenPort controls the public listener port.
	ListenPort int
	// DisplayName is displayed by the service.
	DisplayName string `jsonschema:"description=Explicit display description"`
	// Nested contains connection settings.
	Nested DescribedSchemaNested
}

type describedUnexportedSchemaConfig struct {
	// ListenPort controls the public listener port.
	ListenPort int
}

func TestSchemaIncludesCommentsOnUnexportedConfigTypes(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[describedUnexportedSchemaConfig]()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}

	port := schemaObject(t, schemaProperties(t, data), "listen_port")
	if port["description"] != "ListenPort controls the public listener port." {
		t.Fatalf("listen_port description = %v", port["description"])
	}
}

type DescribedSchemaNested struct {
	// MaxConnections limits simultaneous connections.
	MaxConnections int
}

func TestSchemaIncludesFieldDocComments(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[DescribedSchemaConfig]()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}

	properties := schemaProperties(t, data)

	port := schemaObject(t, properties, "listen_port")
	if port["description"] != "ListenPort controls the public listener port." {
		t.Errorf("listen_port description = %v", port["description"])
	}

	name := schemaObject(t, properties, "display_name")
	if name["description"] != "Explicit display description" {
		t.Errorf("display_name description = %v", name["description"])
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	definitions := schemaObject(t, parsed, "$defs")
	nested := schemaObject(t, definitions, "DescribedSchemaNested")
	nestedProperties := schemaObject(t, nested, "properties")

	maxConnections := schemaObject(t, nestedProperties, "max_connections")
	if maxConnections["description"] != "MaxConnections limits simultaneous connections." {
		t.Errorf("max_connections description = %v", maxConnections["description"])
	}
}

func TestSchemaCommentsDoNotDependOnWorkingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())

	data, err := strata.Schema[DescribedSchemaConfig]()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}

	port := schemaObject(t, schemaProperties(t, data), "listen_port")
	if port["description"] != "ListenPort controls the public listener port." {
		t.Fatalf("listen_port description = %v", port["description"])
	}
}

func TestSchemaCommentsFromExplicitSourceDirectory(t *testing.T) {
	t.Parallel()

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}

	data, err := strata.Schema[DescribedSchemaConfig](strata.WithSchemaComments("github.com/zigai/strata_test", filepath.Dir(sourceFile)))
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}

	port := schemaObject(t, schemaProperties(t, data), "listen_port")
	if port["description"] != "ListenPort controls the public listener port." {
		t.Fatalf("listen_port description = %v", port["description"])
	}
}

func TestSchemaPrimitiveDoesNotRequireSource(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[int]()
	if err != nil {
		t.Fatalf("Schema[int]: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed["type"] != "integer" {
		t.Fatalf("type = %v, want integer", parsed["type"])
	}
}

func TestSchemaIgnoredFieldsDoNotRequireCommentSources(t *testing.T) {
	t.Parallel()

	type config struct {
		Port       int
		JSONHidden *http.Server `json:"-"`
		Hidden     *http.Server `jsonschema:"-"`
	}

	data, err := strata.Schema[config]()
	if err != nil {
		t.Fatal(err)
	}

	if properties := schemaProperties(t, data); len(properties) != 1 || schemaObject(t, properties, "port")["type"] != "integer" {
		t.Fatalf("properties = %v, want only integer port", properties)
	}
}

func TestSchemaReportsUnavailableCommentSource(t *testing.T) {
	t.Parallel()

	_, err := strata.Schema[DescribedSchemaConfig](strata.WithSchemaComments("github.com/zigai/strata_test", filepath.Join(t.TempDir(), "missing")))
	if !errors.Is(err, strata.ErrReflectSchema) {
		t.Fatalf("Schema error = %v, want ErrReflectSchema", err)
	}
}

type chanConfig struct {
	Ch chan int `strata:"ch"`
}

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

func schemaObject(t *testing.T, document map[string]any, key string) map[string]any {
	t.Helper()

	object, ok := document[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %v, want a schema object", key, document[key])
	}

	return object
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
