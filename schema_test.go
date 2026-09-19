package strata_test

import (
	"encoding/json/v2"
	"errors"
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
