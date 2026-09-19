package edit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zigai/strata/internal/edit"
)

func TestUpdateTOMLPreservesCommentsAndFormatting(t *testing.T) {
	t.Parallel()

	input := `# Top level comment

[server]
# Host comment
host = "127.0.0.1" # local interface
port = 8080 # web port

[database]
# Database comment
port = 5432 # pg port
`

	updated, err := edit.UpdateTOML([]byte(input), "database.port", 5433)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	result := string(updated)

	// The value is replaced in place, and the inline comment is kept.
	if !strings.Contains(result, "port = 5433 # pg port") {
		t.Errorf("expected port = 5433 # pg port in result, got:\n%s", result)
	}

	// Comments elsewhere in the document are preserved.
	if !strings.Contains(result, "# Top level comment") {
		t.Errorf("expected # Top level comment preserved, got:\n%s", result)
	}

	if !strings.Contains(result, "# Database comment") {
		t.Errorf("expected # Database comment preserved, got:\n%s", result)
	}

	if !strings.Contains(result, "port = 8080 # web port") {
		t.Errorf("expected server port untouched, got:\n%s", result)
	}

	// A key that is absent is appended.
	appended, err := edit.UpdateTOML([]byte(input), "metrics.enabled", true)
	if err != nil {
		t.Fatalf("UpdateTOML append error: %v", err)
	}

	if !strings.Contains(string(appended), "enabled = true") {
		t.Errorf("expected appended key in TOML, got:\n%s", string(appended))
	}
}

func TestUpdateTOMLInlineCommentHeader(t *testing.T) {
	t.Parallel()

	input := `[server] # server settings
port = 8080
`

	updated, err := edit.UpdateTOML([]byte(input), "server.port", 9000)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	if !strings.Contains(string(updated), "port = 9000") {
		t.Errorf("expected port = 9000, got:\n%s", string(updated))
	}
}

func TestUpdateTOMLMultilineString(t *testing.T) {
	t.Parallel()

	input := `val = """
line 1
line 2
"""
other = "keep"
`

	updated, err := edit.UpdateTOML([]byte(input), "val", "single line")
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	res := string(updated)
	if strings.Contains(res, "line 1") || strings.Contains(res, "line 2") {
		t.Errorf("multiline tail was not removed:\n%s", res)
	}

	if !strings.Contains(res, "val = 'single line'") && !strings.Contains(res, "val = \"single line\"") {
		t.Errorf("missing new value in result:\n%s", res)
	}
}

func TestUpdateTOMLAppendIntoCorrectTable(t *testing.T) {
	t.Parallel()

	input := `[server]
port = 8080

[database]
port = 5432
`

	updated, err := edit.UpdateTOML([]byte(input), "server.timeout", 30)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	res := string(updated)
	serverIdx := strings.Index(res, "[server]")
	timeoutIdx := strings.Index(res, "timeout = 30")
	dbIdx := strings.Index(res, "[database]")

	if serverIdx == -1 || timeoutIdx == -1 || dbIdx == -1 {
		t.Fatalf("missing expected sections in:\n%s", res)
	}

	if serverIdx >= timeoutIdx || timeoutIdx >= dbIdx {
		t.Errorf("timeout = 30 should be between [server] and [database], got:\n%s", res)
	}
}

func TestAppendTOMLKey(t *testing.T) {
	t.Parallel()

	initial := `# Configuration
[server]
port = 8080
`

	// NB: the fixture already declares a [server] table, and server.host is
	// appended inside it.
	updated, err := edit.UpdateTOML([]byte(initial), "server.host", "0.0.0.0")
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	if !strings.Contains(string(updated), "host = \"0.0.0.0\"") && !strings.Contains(string(updated), "host = '0.0.0.0'") {
		t.Errorf("expected appended host, got:\n%s", string(updated))
	}

	// NB: the fixture has no version key and no table to hold one; the update
	// appends it at the top level.
	updatedTop, err := edit.UpdateTOML([]byte(initial), "version", 2)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	if !strings.Contains(string(updatedTop), "version = 2") {
		t.Errorf("expected appended version, got:\n%s", string(updatedTop))
	}
}

func TestTOMLQuotedTableHeader(t *testing.T) {
	t.Parallel()

	input := `[table."sub.key"]
foo = "bar"
`

	updated, err := edit.UpdateTOML([]byte(input), "table.sub.key.foo", "updated")
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	result := string(updated)
	if strings.Contains(result, "[table.sub.key]") {
		t.Errorf("duplicate table header created:\n%s", result)
	}

	if !strings.Contains(result, "foo = 'updated'") && !strings.Contains(result, "foo = \"updated\"") {
		t.Errorf("foo not updated in-place:\n%s", result)
	}
}

func TestTOMLArrayOfTablesDoesNotCorruptPreviousTable(t *testing.T) {
	t.Parallel()

	input := `[server]
port = 8080

[[items]]
name = "first"
`

	updated, err := edit.UpdateTOML([]byte(input), "server.port", 9000)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	result := string(updated)
	if !strings.Contains(result, "port = 9000") {
		t.Errorf("server.port not updated:\n%s", result)
	}

	if !strings.Contains(result, "name = \"first\"") {
		t.Errorf("items.name corrupted:\n%s", result)
	}
}

func TestUpdateTOMLIntermediateScalarReturnsError(t *testing.T) {
	t.Parallel()

	input := "a = 1\n"

	_, err := edit.UpdateTOML([]byte(input), "a.b", 2)
	if err == nil {
		t.Fatalf("expected error when setting dotted key on scalar, got nil")
	}

	if !errors.Is(err, edit.ErrNonObjectNavigation) {
		t.Errorf("expected ErrNonObjectNavigation, got %v", err)
	}
}

func TestUpdateTOMLInlineTable(t *testing.T) {
	t.Parallel()

	input := "server = { port = 8080, host = \"localhost\" }\n"

	updated, err := edit.UpdateTOML([]byte(input), "server.port", 9090)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	result := string(updated)
	if strings.Contains(result, "[server]") {
		t.Errorf("created duplicate table header instead of updating inline table:\n%s", result)
	}

	if !strings.Contains(result, "port = 9090") {
		t.Errorf("port not updated in inline table:\n%s", result)
	}
}

func TestUpdateTOMLMultilineArrayNotTableHeader(t *testing.T) {
	t.Parallel()

	input := `[server]
matrix = [
  [1, 2],
  [3, 4]
]
`

	updated, err := edit.UpdateTOML([]byte(input), "server.port", 8080)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	result := string(updated)
	if strings.Contains(result, "[1, 2],\nport = 8080") {
		t.Errorf("inserted key inside multiline array:\n%s", result)
	}
}

func TestUpdateTOMLQuotedKeyPreserved(t *testing.T) {
	t.Parallel()

	input := "name = \"app\"\n"

	updated, err := edit.UpdateTOML([]byte(input), "\"foo.bar\"", 2)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	result := string(updated)
	if !strings.Contains(result, "\"foo.bar\" = 2") {
		t.Errorf("expected \"foo.bar\" = 2, got:\n%s", result)
	}
}

func TestUpdateTOMLSingleQuotedStringBackslash(t *testing.T) {
	t.Parallel()

	input := "path = 'C:\\dir\\' # comment\n"

	updated, err := edit.UpdateTOML([]byte(input), "port", 8080)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	result := string(updated)
	if !strings.Contains(result, "# comment") {
		t.Errorf("comment was corrupted:\n%s", result)
	}
}

func TestUpdateTOMLSliceOfMaps(t *testing.T) {
	t.Parallel()

	input := "app = \"test\"\n"
	routes := []map[string]any{{"path": "/api"}}

	updated, err := edit.UpdateTOML([]byte(input), "routes", routes)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	result := string(updated)
	if strings.Contains(result, "_k") {
		t.Errorf("leaked dummy _k identifier in output:\n%s", result)
	}
}
