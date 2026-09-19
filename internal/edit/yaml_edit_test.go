package edit_test

import (
	"strings"
	"testing"

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

	// The targeted key takes the new value.
	if !strings.Contains(result, "port: 9090") {
		t.Errorf("expected port: 9090 in result, got:\n%s", result)
	}

	// Comments elsewhere in the document are preserved.
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

	// A key that is absent is appended.
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

func TestYAMLCaseSensitiveKeyMatching(t *testing.T) {
	t.Parallel()

	input := `Port: 8080
port: 9090
`

	updated, err := edit.UpdateYAML([]byte(input), "port", 9999)
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
