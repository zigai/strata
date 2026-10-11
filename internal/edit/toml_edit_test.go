package edit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/zigai/strata/internal/edit"
)

const portKey = "port"

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

	if !strings.Contains(result, "port = 5433 # pg port") {
		t.Errorf("expected port = 5433 # pg port in result, got:\n%s", result)
	}

	if !strings.Contains(result, "# Top level comment") {
		t.Errorf("expected # Top level comment preserved, got:\n%s", result)
	}

	if !strings.Contains(result, "# Database comment") {
		t.Errorf("expected # Database comment preserved, got:\n%s", result)
	}

	if !strings.Contains(result, "port = 8080 # web port") {
		t.Errorf("expected server port untouched, got:\n%s", result)
	}

	appended, err := edit.UpdateTOML([]byte(input), "metrics.enabled", true)
	if err != nil {
		t.Fatalf("UpdateTOML append error: %v", err)
	}

	if !strings.Contains(string(appended), "enabled = true") {
		t.Errorf("expected appended key in TOML, got:\n%s", string(appended))
	}
}

func TestUpdateTOMLCompositeSubtreesPreserveSiblings(t *testing.T) {
	t.Parallel()

	type item struct {
		HTTPPort int `strata:"listen_port"`
	}

	for _, test := range []struct{ name, input string }{
		{"inline", "service = { ports = { old = { listen_port = 1 } }, keep = 7 } # service comment\n[other]\nkeep = 8 # other comment\n"},
		{"tables", "[service.ports.old]\nlisten_port = 1\n[service]\nkeep = 7 # service comment\n[other]\nkeep = 8 # other comment\n"},
		{"dotted", "service.ports.old.listen_port = 1\nservice.keep = 7 # service comment\n[other]\nkeep = 8 # other comment\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			updated, err := edit.UpdateTOML([]byte(test.input), "service.ports", map[string]item{"first": {HTTPPort: 9090}})
			if err != nil {
				t.Fatal(err)
			}

			var got struct {
				Service struct {
					Ports map[string]struct {
						ListenPort int `toml:"listen_port"`
					}
					Keep int
				}
				Other struct{ Keep int }
			}
			if err := toml.Unmarshal(updated, &got); err != nil {
				t.Fatal(err)
			}

			if len(got.Service.Ports) != 1 || got.Service.Ports["first"].ListenPort != 9090 || got.Service.Keep != 7 || got.Other.Keep != 8 {
				t.Fatalf("document = %+v\n%s", got, updated)
			}

			if !strings.Contains(string(updated), "# service comment") || !strings.Contains(string(updated), "keep = 8 # other comment") {
				t.Fatalf("sibling comments changed:\n%s", updated)
			}
		})
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

func TestUpdateTOMLDecodesEscapedKeys(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ name, input, key string }{
		{"assignment", "\"\\u0061\" = 1\nkeep = 2\n", "a"},
		{"table", "[\"\\u0061\"]\nx = 1\nkeep = 2\n", "a.x"},
		{"long escape", "\"\\U00000061\" = 1\nkeep = 2\n", "a"},
		{"escaped quote", "\"a\\\"b\" = 1\nkeep = 2\n", `a"b`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			updated, err := edit.UpdateTOML([]byte(test.input), test.key, 3)
			if err != nil {
				t.Fatal(err)
			}

			var got map[string]any
			if err := toml.Unmarshal(updated, &got); err != nil {
				t.Fatal(err)
			}

			if test.name == "table" {
				nested, ok := got["a"].(map[string]any)
				if !ok || nested["x"] != int64(3) || nested["keep"] != int64(2) {
					t.Fatalf("table = %#v", got)
				}
			} else if got[test.key] != int64(3) || got["keep"] != int64(2) {
				t.Fatalf("document = %#v", got)
			}

			if !strings.Contains(string(updated), "keep = 2") {
				t.Fatalf("untouched assignment changed:\n%s", updated)
			}
		})
	}
}

func TestUpdateTOMLRetainsMultilineClosingComment(t *testing.T) {
	t.Parallel()

	for _, delimiter := range []string{`"""`, "'''"} {
		t.Run(delimiter, func(t *testing.T) {
			t.Parallel()

			input := "message = " + delimiter + "\nold # text, not a comment\n" + delimiter + " # keep closing comment\nkeep = 2\n"

			updated, err := edit.UpdateTOML([]byte(input), "message", "new")
			if err != nil {
				t.Fatal(err)
			}

			if strings.Count(string(updated), "# keep closing comment") != 1 {
				t.Fatalf("closing comment lost or duplicated:\n%s", updated)
			}

			var got map[string]any
			if err := toml.Unmarshal(updated, &got); err != nil {
				t.Fatal(err)
			}

			if got["message"] != "new" || got["keep"] != int64(2) || strings.Contains(string(updated), "# text") {
				t.Fatalf("document = %#v\n%s", got, updated)
			}
		})
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

	updated, err := edit.UpdateTOML([]byte(initial), "server.host", "0.0.0.0")
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	if !strings.Contains(string(updated), "host = \"0.0.0.0\"") && !strings.Contains(string(updated), "host = '0.0.0.0'") {
		t.Errorf("expected appended host, got:\n%s", string(updated))
	}

	updatedTop, err := edit.UpdateTOML([]byte(initial), "version", 2)
	if err != nil {
		t.Fatalf("UpdateTOML error: %v", err)
	}

	if !strings.Contains(string(updatedTop), "version = 2") {
		t.Errorf("expected appended version, got:\n%s", string(updatedTop))
	}
}

func TestUpdateTOMLPreservesLiteralDottedSibling(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"[a.\"b.c\"]\nx = 1\n[a]\nkeep = 2\n",
		"[a.'b.c']\nx = 1\n[a]\nkeep = 2\n",
		"[a.\"b\\u002ec\"]\nx = 1\n[a]\nkeep = 2\n",
		"[a.\"b.c\"]\nx = 1\n[a.b.old]\nx = 4\n[a]\nkeep = 2\n",
	} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			updated, err := edit.UpdateTOML([]byte(input), "a.b", 3)
			if err != nil {
				t.Fatal(err)
			}

			var got struct {
				A struct {
					B       int
					Literal struct{ X int } `toml:"b.c"`
					Keep    int
				}
			}
			if err := toml.Unmarshal(updated, &got); err != nil {
				t.Fatal(err)
			}

			if got.A.B != 3 || got.A.Literal.X != 1 || got.A.Keep != 2 {
				t.Fatalf("document = %+v\n%s", got, updated)
			}

			if !strings.Contains(string(updated), "x = 1") || !strings.Contains(string(updated), "keep = 2") {
				t.Fatalf("untouched assignments changed:\n%s", updated)
			}
		})
	}
}

func TestTOMLQuotedTableHeader(t *testing.T) {
	t.Parallel()

	input := `[table."sub.key"]
foo = "bar"
`

	updated, err := edit.UpdateTOML([]byte(input), `table."sub.key".foo`, "updated")
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

	if !errors.Is(err, edit.ErrPathNotMapping) {
		t.Errorf("expected ErrPathNotMapping, got %v", err)
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

	updated, err := edit.UpdateTOML([]byte(input), portKey, 8080)
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

func TestUpdateTOMLMultilineEdits(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, input, key, literal, want string }{
		{"unrelated_string", "banner = \"\"\"\nport = 123\n\"\"\"\nport = 8080\n", portKey, "9000", "banner = \"\"\"\nport = 123\n\"\"\"\nport = 9000\n"},
		{"multiline_array", "ports = [\n  80,\n  443,\n]\nkeep = true\n", "ports", "[ 8080 ]", "ports = [ 8080 ]\nkeep = true\n"},
		{"single_line_triple_with_comment", "banner = \"\"\"hello\"\"\" # keep comment\nport = 8080\n", "banner", "\"new\"", "banner = \"new\" # keep comment\nport = 8080\n"},
		{"single_line_triple_with_quote_and_bracket", "a = '''don't ['''\n\n[db]\nport = 1\n", "a", "'x'", "a = 'x'\n\n[db]\nport = 1\n"},
		{"table_after_single_line_triple", "a = '''don't ['''\n\n[db]\nport = 1\n", "db.port", "2", "a = '''don't ['''\n\n[db]\nport = 2\n"},
		{"bracket_inside_string_in_array", "a = [\n  \"\"\"\n]\nb = 1\n  \"\"\",\n]\nb = 1\n", "b", "2", "a = [\n  \"\"\"\n]\nb = 1\n  \"\"\",\n]\nb = 2\n"},
		{"space_in_quoted_key", "\"my key\" = 1\n", "my key", "2", "\"my key\" = 2\n"},
		{"spaces_around_dots", "a . b = 1\n", "a.b", "2", "a . b = 2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := edit.UpdateTOMLFormatted([]byte(tc.input), tc.key, nil, tc.literal)
			if err != nil {
				t.Fatal(err)
			}

			if string(got) != tc.want {
				t.Errorf("incorrect edit:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestUpdateTOMLEmptyKey(t *testing.T) {
	t.Parallel()

	var (
		panicked any
		err      error
	)

	func() {
		defer func() { panicked = recover() }()

		_, err = edit.UpdateTOML([]byte("port = 8080\n"), "", 1)
	}()

	if panicked != nil {
		t.Errorf("empty key panics instead of returning an error: %v", panicked)
	} else if err == nil {
		t.Error("empty key accepted")
	}
}

func TestUpdateTOMLCaseSensitiveExactMatchPreference(t *testing.T) {
	t.Parallel()

	in := "Port = 1\nport = 2\n"

	got, err := edit.UpdateTOML([]byte(in), portKey, 3)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(got), "Port = 1\nport = 3") {
		t.Errorf("wrong case-sensitive key updated:\n%s", got)
	}
}

func TestUpdateTOMLKeepsEverythingButAlignment(t *testing.T) {
	t.Parallel()

	const document = `[server]
port = 8080          # port
host = "0.0.0.0"

  indented = true

multiline = """
line one
"""

arr = [
  "a",
  "b",
]
`

	updated, err := edit.UpdateTOML([]byte(document), "server.port", 9090)
	if err != nil {
		t.Fatalf("UpdateTOML: %v", err)
	}

	got := string(updated)

	for _, want := range []string{`"""`, "line one", "  \"a\",\n  \"b\",", "  indented = true"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lost %q:\n%s", want, got)
		}
	}

	if !strings.Contains(got, "port = 9090 # port") {
		t.Errorf("trailing comment padding was not collapsed:\n%s", got)
	}
}
