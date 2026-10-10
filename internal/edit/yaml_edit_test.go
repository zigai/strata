package edit_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata/codec"
	"github.com/zigai/strata/internal/edit"
)

func TestUpdateYAMLPreservesComments(t *testing.T) {
	t.Parallel()

	input := `# Main configuration
server:
  # Host comment
  host: 127.0.0.1 # inline host
  port: 8080 # inline port

database:
  # DB comment
  port: 5432
`

	updated, err := edit.UpdateYAML([]byte(input), "server.port", 9090)
	if err != nil {
		t.Fatalf("UpdateYAML error: %v", err)
	}

	result := string(updated)

	if !strings.Contains(result, "port: 9090") {
		t.Errorf("expected port: 9090 in result, got:\n%s", result)
	}

	if !strings.Contains(result, "# Main configuration") {
		t.Errorf("expected # Main configuration comment preserved, got:\n%s", result)
	}

	if !strings.Contains(result, "# Host comment") {
		t.Errorf("expected # Host comment preserved, got:\n%s", result)
	}

	if !strings.Contains(result, "# DB comment") {
		t.Errorf("expected # DB comment preserved, got:\n%s", result)
	}

	if !strings.Contains(result, "host: 127.0.0.1") {
		t.Errorf("expected host: 127.0.0.1 preserved, got:\n%s", result)
	}

	withAppended, err := edit.UpdateYAML([]byte(input), "server.timeout", "30s")
	if err != nil {
		t.Fatalf("UpdateYAML append error: %v", err)
	}

	if !strings.Contains(string(withAppended), "timeout: 30s") {
		t.Errorf("expected appended timeout, got:\n%s", string(withAppended))
	}
}

func TestYAMLPreservesAliasFields(t *testing.T) {
	t.Parallel()

	input := `default: &default
  timeout: 30s
  retries: 3

production: *default
`

	updated, err := edit.UpdateYAML([]byte(input), "production.retries", 5)
	if err != nil {
		t.Fatalf("UpdateYAML error: %v", err)
	}

	result := string(updated)

	if !strings.Contains(result, "timeout: 30s") {
		t.Errorf("expected production to retain timeout: 30s from alias, got:\n%s", result)
	}

	if !strings.Contains(result, "retries: 5") {
		t.Errorf("expected production.retries to be 5, got:\n%s", result)
	}
}

func TestUpdateYAMLRetainsAliasComment(t *testing.T) {
	t.Parallel()

	input := "base: &base\n  x: 1\n  y: 2\ncopy: *base # keep alias comment\n"

	updated, err := edit.UpdateYAML([]byte(input), "copy.x", 3)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Count(string(updated), "# keep alias comment") != 1 {
		t.Fatalf("alias comment lost or duplicated:\n%s", updated)
	}

	var got map[string]map[string]int
	if err := yaml.Unmarshal(updated, &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]map[string]int{"base": {"x": 1, "y": 2}, "copy": {"x": 3, "y": 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("document = %v, want %v", got, want)
	}
}

func TestUpdateYAMLRejectsDanglingAliases(t *testing.T) {
	t.Parallel()

	input := []byte("base:\n  x: &value 1\ncopy: *value\n")

	updated, err := edit.UpdateYAML(input, "base", map[string]int{"y": 2})
	if !errors.Is(err, codec.ErrMalformed) || updated != nil {
		t.Fatalf("UpdateYAML = %q, %v, want nil and ErrMalformed", updated, err)
	}

	if string(input) != "base:\n  x: &value 1\ncopy: *value\n" {
		t.Fatalf("input changed: %s", input)
	}
}

func TestYAMLCaseSensitiveKeyMatching(t *testing.T) {
	t.Parallel()

	input := `Port: 8080
port: 9090
`

	updated, err := edit.UpdateYAML([]byte(input), portKey, 9999)
	if err != nil {
		t.Fatalf("UpdateYAML error: %v", err)
	}

	result := string(updated)
	if !strings.Contains(result, "Port: 8080") {
		t.Errorf("expected Port: 8080 untouched, got:\n%s", result)
	}

	if !strings.Contains(result, "port: 9999") {
		t.Errorf("expected port: 9999 updated, got:\n%s", result)
	}
}

func TestUpdateYAMLAnchorPreserved(t *testing.T) {
	t.Parallel()

	input := "base: &base 10\ncopy: *base\n"

	updated, err := edit.UpdateYAML([]byte(input), "base", 20)
	if err != nil {
		t.Fatalf("UpdateYAML error: %v", err)
	}

	result := string(updated)
	if !strings.Contains(result, "&base") {
		t.Errorf("anchor was lost:\n%s", result)
	}
}

func TestUpdateYAMLAliasDeepClone(t *testing.T) {
	t.Parallel()

	input := `default: &default
  nested:
    val: 10
server: *default
`

	updated, err := edit.UpdateYAML([]byte(input), "server.nested.val", 999)
	if err != nil {
		t.Fatalf("UpdateYAML error: %v", err)
	}

	result := string(updated)
	if strings.Contains(result, "val: 999") && strings.Contains(result, "default:\n  nested:\n    val: 999") {
		t.Errorf("mutating server.nested.val corrupted default.nested.val:\n%s", result)
	}
}
