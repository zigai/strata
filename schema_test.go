package strata_test

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/invopop/jsonschema"

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

// A build that keeps absolute source paths finds comments from any working
// directory. A -trimpath build has only module paths, so it looks in the
// module holding the working directory and reports the comments as unavailable
// outside it.
func TestSchemaCommentsWorkingDirectoryDependsOnSourcePaths(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}

	t.Chdir(t.TempDir())

	data, err := strata.Schema[DescribedSchemaConfig]()
	if !filepath.IsAbs(sourceFile) {
		if !errors.Is(err, strata.ErrReflectSchema) || !strings.Contains(err.Error(), "WithSchemaComments") {
			t.Fatalf("Schema from outside the module of a trimmed build: err = %v, want ErrReflectSchema naming WithSchemaComments", err)
		}

		return
	}

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

	// go test runs in the package directory, which holds this test's source
	// whether or not the build trims source paths.
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	data, err := strata.Schema[DescribedSchemaConfig](strata.WithSchemaComments("github.com/zigai/strata_test", directory))
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

	properties := schemaProperties(t, data)
	delete(properties, "$schema")

	if len(properties) != 1 || schemaObject(t, properties, "port")["type"] != "integer" {
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

func TestSchemaPropertiesAreConfigurationKeys(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[schemaKeysConfig]()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}

	props := schemaProperties(t, data)

	got := slices.Sorted(maps.Keys(props))
	if want := []string{"$schema", "listen", "nested", "plain", "tname", "yname"}; !slices.Equal(got, want) {
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

type SchemaTemplateNested struct {
	Port int
}

type schemaTemplateConfig struct {
	SchemaTemplateNested `json:"db"`

	Address string `strata:"addr"`
	Timeout time.Duration
	Grace   strata.Duration
	Parent  *SchemaTemplateNested
}

func (c *schemaTemplateConfig) SetDefaults() {
	c.Timeout = 30 * time.Second
}

// The schema an Init template links must describe that template: its keys,
// its duration strings, its null pointers, and the $schema property itself.
func TestSchemaDescribesInitTemplates(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[schemaTemplateConfig]()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}

	properties := schemaProperties(t, data)

	path := filepath.Join(t.TempDir(), "config.json")
	if err := strata.Init[schemaTemplateConfig](path, strata.WithSchemaURL("https://example.invalid/config.schema.json")); err != nil {
		t.Fatalf("Init: %v", err)
	}

	template, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var document map[string]any
	if err := json.Unmarshal(template, &document); err != nil {
		t.Fatal(err)
	}

	if got, want := slices.Sorted(maps.Keys(document)), slices.Sorted(maps.Keys(properties)); !slices.Equal(got, want) {
		t.Fatalf("template keys = %v, schema properties = %v", got, want)
	}

	if _, ok := properties["db"]; !ok {
		t.Errorf("properties = %v, want the tagged embedded struct as db", properties)
	}

	for _, key := range []string{"$schema", "addr", "timeout", "grace"} {
		if got := schemaObject(t, properties, key)["type"]; got != "string" {
			t.Errorf("%s type = %v, want string", key, got)
		}
	}

	parent := schemaObject(t, properties, "parent")
	variants, _ := parent["anyOf"].([]any)

	if len(variants) != 2 || !reflect.DeepEqual(variants[1], map[string]any{"type": "null"}) {
		t.Errorf("parent = %v, want the struct or null", parent)
	}
}

type schemaToken struct{ Value string }

func (t schemaToken) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(t.Value)
	if err != nil {
		return nil, fmt.Errorf("encode token: %w", err)
	}

	return data, nil
}

func (t *schemaToken) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &t.Value); err != nil {
		return fmt.Errorf("decode token: %w", err)
	}

	return nil
}

func (schemaToken) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Pattern: "^[a-z]+$"}
}

type schemaPointerToken struct{ Value string }

func (*schemaPointerToken) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", MinLength: new(uint64(2))}
}

type schemaExtended struct {
	Port int
}

func (schemaExtended) JSONSchemaExtend(schema *jsonschema.Schema) {
	schema.Title = "extended"
}

type schemaHookConfig struct {
	Token    schemaToken
	Pointer  *schemaToken
	Receiver schemaPointerToken
	Extended schemaExtended
}

func TestSchemaUsesTypesOwnSchemaMethods(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[schemaHookConfig]()
	if err != nil {
		t.Fatal(err)
	}

	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}

	properties := schemaObject(t, document, "properties")
	token := map[string]any{"type": "string", "pattern": "^[a-z]+$"}

	if got := schemaObject(t, properties, "token"); !reflect.DeepEqual(got, token) {
		t.Errorf("token = %v, want %v", got, token)
	}

	if got, want := schemaObject(t, properties, "pointer"), map[string]any{"anyOf": []any{token, map[string]any{"type": "null"}}}; !reflect.DeepEqual(got, want) {
		t.Errorf("pointer = %v, want %v", got, want)
	}

	if got, want := schemaObject(t, properties, "receiver"), map[string]any{"type": "string", "minLength": float64(2)}; !reflect.DeepEqual(got, want) {
		t.Errorf("receiver = %v, want %v", got, want)
	}

	extended := schemaObject(t, schemaObject(t, document, "$defs"), "schemaExtended")
	if extended["title"] != "extended" {
		t.Errorf("schemaExtended = %v, want the title its JSONSchemaExtend sets", extended)
	}
}

type inlineSchemaConfig struct {
	Server inlineServer    `yaml:",inline"`
	Extra  map[string]bool `strata:",inline"`
	Name   string
}

func TestSchemaDescribesInlineFieldsAtTheParentLevel(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[inlineSchemaConfig]()
	if err != nil {
		t.Fatal(err)
	}

	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}

	properties := schemaObject(t, document, "properties")
	if got, want := slices.Sorted(maps.Keys(properties)), []string{"$schema", "host", "name", "port"}; !slices.Equal(got, want) {
		t.Fatalf("properties = %v, want %v", got, want)
	}

	if got, want := schemaObject(t, document, "additionalProperties"), map[string]any{"type": "boolean"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("additionalProperties = %v, want %v from the inline map", got, want)
	}
}

type schemaPayload struct {
	Port int
}

type schemaAliasConfig struct {
	Payload any              `json:"wire_payload" strata:"wire_payload"`
	Ref     *any             `strata:"ref"`
	Name    string           `jsonschema:"description=tag description"`
	Cleared string           `jsonschema:"description=cleared description"`
	Owned   schemaOwnedHooks `strata:",inline"`
}

func (schemaAliasConfig) JSONSchemaProperty(name string) any {
	switch name {
	case "wire_payload", "ref":
		return schemaPayload{}
	case "name":
		return 0
	}

	return nil
}

func (schemaAliasConfig) GetFieldDocString(name string) string {
	if name == "Name" {
		return "hook description"
	}

	return ""
}

type schemaOwnedHooks struct {
	Level string `jsonschema:"description=level tag"`
	Mode  string
}

func (*schemaOwnedHooks) JSONSchemaProperty(name string) any {
	if name == "mode" {
		return true
	}

	return nil
}

func (*schemaOwnedHooks) GetFieldDocString(name string) string {
	if name == "Level" {
		return "level hook"
	}

	return ""
}

func TestSchemaHonorsPropertyHooksOnTheDeclaringType(t *testing.T) {
	t.Parallel()

	data, err := strata.Schema[schemaAliasConfig]()
	if err != nil {
		t.Fatal(err)
	}

	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}

	properties := schemaObject(t, document, "properties")
	ref := map[string]any{"$ref": "#/$defs/schemaPayload"}

	for key, want := range map[string]any{
		"wire_payload": ref,
		"ref":          map[string]any{"anyOf": []any{ref, map[string]any{"type": "null"}}},
		"name":         map[string]any{"type": "integer", "description": "hook description"},
		"cleared":      map[string]any{"type": "string"},
		"level":        map[string]any{"type": "string", "description": "level hook"},
		"mode":         map[string]any{"type": "boolean"},
	} {
		if got := schemaObject(t, properties, key); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}

	payload := schemaObject(t, schemaObject(t, schemaObject(t, document, "$defs"), "schemaPayload"), "properties")
	if _, ok := payload["port"]; !ok {
		t.Errorf("schemaPayload properties = %v, want the port key", payload)
	}
}
