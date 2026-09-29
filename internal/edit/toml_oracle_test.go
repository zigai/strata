package edit_test

import (
	"errors"
	"maps"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/zigai/strata/internal/edit"
)

// requireTOMLEdit checks an edit against go-toml as the reference: output
// decodes to input's document with only the value at path replaced by value.
// Every other key, at any depth, must keep its decoded value.
func requireTOMLEdit(t *testing.T, input, output []byte, path []string, value any) {
	t.Helper()

	var before, after map[string]any
	if err := toml.Unmarshal(input, &before); err != nil {
		t.Fatalf("input does not parse: %v\n%s", err, input)
	}

	if err := toml.Unmarshal(output, &after); err != nil {
		t.Fatalf("output does not parse: %v\n%s", err, output)
	}

	want := setDecoded(t, before, path, decodedTOMLValue(t, value))
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("edit of %q changed the document wrongly\ninput:\n%s\noutput:\n%s\ndecoded: %#v\nwant:    %#v",
			strings.Join(path, "."), input, output, after, want)
	}
}

// decodedTOMLValue is value as go-toml decodes it back, such as int64 for an
// int and []any for a slice.
func decodedTOMLValue(t *testing.T, value any) any {
	t.Helper()

	data, err := toml.Marshal(map[string]any{"v": value})
	if err != nil {
		t.Fatalf("reference encode %#v: %v", value, err)
	}

	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("reference decode %s: %v", data, err)
	}

	return doc["v"]
}

// setDecoded returns a deep copy of doc with value at path, creating tables
// on the way.
func setDecoded(t *testing.T, doc map[string]any, path []string, value any) map[string]any {
	t.Helper()

	out := make(map[string]any, len(doc)+1)
	maps.Copy(out, doc)

	if len(path) == 1 {
		out[path[0]] = value

		return out
	}

	child, _ := out[path[0]].(map[string]any)
	out[path[0]] = setDecoded(t, child, path[1:], value)

	return out
}

// Each regression document the editor once broke, checked with the reference
// oracle so an edit that also moved a sibling value fails here.
func TestUpdateTOMLChangesOnlyTheNamedKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		input string
		key   string
		path  []string
		value any
	}{
		{"nested table value", "# top\n[server]\nhost = \"127.0.0.1\" # local\nport = 8080\n\n[database]\nport = 5432 # pg\n", "database.port", []string{"database", portKey}, 5433},
		{"append to existing table", "[server]\nport = 8080\n\n[database]\nport = 5432\n", "server.timeout", []string{"server", "timeout"}, 30},
		{"append new table", "[server]\nport = 8080\n", "metrics.enabled", []string{"metrics", "enabled"}, true},
		{"append root key after tables", "# c\n[server]\nport = 8080\n", "version", []string{"version"}, 2},
		{"header with inline comment", "[server] # settings\nport = 8080\nhost = \"h\"\n", "server.port", []string{"server", portKey}, 9000},
		{"multi-line string replaced", "val = \"\"\"\nline 1\nline 2\n\"\"\"\nother = \"keep\"\n", "val", []string{"val"}, "single line"},
		{"quoted table header", "[table.\"sub.key\"]\nfoo = \"bar\"\nkeep = 1\n", "table.sub.key.foo", []string{"table", "sub.key", "foo"}, "updated"},
		{"array of tables after the table", "[server]\nport = 8080\n\n[[items]]\nname = \"first\"\n\n[[items]]\nname = \"second\"\n", "server.port", []string{"server", portKey}, 9000},
		{"inline table", "server = { port = 8080, host = \"localhost\" }\nother = 1\n", "server.port", []string{"server", portKey}, 9090},
		{"multi-line array in table", "[server]\nmatrix = [\n  [1, 2],\n  [3, 4]\n]\n", "server.port", []string{"server", portKey}, 8080},
		{"quoted key", "name = \"app\"\n", "\"foo.bar\"", []string{"foo.bar"}, 2},
		{"single-quoted backslash", "path = 'C:\\dir\\' # comment\nname = 'x'\n", portKey, []string{portKey}, 8080},
		{"slice of maps", "app = \"test\"\n", "routes", []string{"routes"}, []map[string]any{{"path": "/api"}}},
		{"case-different sibling", "Port = 1\nport = 2\n", portKey, []string{portKey}, 3},
		{"string mentioning the key", "banner = \"\"\"\nport = 123\n\"\"\"\nport = 8080\n", portKey, []string{portKey}, 9000},
		{"dotted keys at root", "a.b = 1\na.c = 2\n", "a.b", []string{"a", "b"}, 5},
		{"crlf line endings", "[s]\r\nx = 1\r\ny = 2\r\n", "s.y", []string{"s", "y"}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := edit.UpdateTOML([]byte(tc.input), tc.key, tc.value)
			if err != nil {
				t.Fatalf("UpdateTOML: %v", err)
			}

			requireTOMLEdit(t, []byte(tc.input), out, tc.path, tc.value)
		})
	}
}

var (
	bareTOMLKey = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)

	// multilineInlineTable matches an inline table that spans lines, which TOML
	// 1.0 forbids and go-toml accepts; the editor handles one-line tables only.
	multilineInlineTable = regexp.MustCompile(`=\s*\{[^}]*\n`)

	// quotedDottedKey matches a quoted key or header segment holding a dot, which
	// the editor merges with dotted keys on purpose (see canonicalHeaderParts).
	quotedDottedKey = regexp.MustCompile(`(?m)^\s*(\[+[^\]\n]*|[^=\n]*)["'][^"'\n]*\.[^"'\n]*["'][^=\n]*(\]|=)`)
)

// For any document go-toml accepts and any bare dotted key whose parent path
// holds tables, UpdateTOML succeeds and changes nothing but that key. A path
// through a scalar must be refused rather than rewritten. Replacing a table or
// an array with a scalar may be refused, and must be exact when it is not.
func FuzzUpdateTOML(f *testing.F) {
	for _, seed := range []struct {
		doc, key string
	}{
		{"# top\n[server]\nhost = \"127.0.0.1\" # local\nport = 8080\n\n[database]\nport = 5432 # pg\n", "database.port"},
		{"[server]\nport = 8080\n\n[database]\nport = 5432\n", "server.timeout"},
		{"[server] # settings\nport = 8080\n", "server.port"},
		{"val = \"\"\"\nline 1\nline 2\n\"\"\"\nother = \"keep\"\n", "val"},
		{"[server]\nport = 8080\n\n[[items]]\nname = \"first\"\n", "server.port"},
		{"server = { port = 8080, host = \"localhost\" }\n", "server.port"},
		{"[server]\nmatrix = [\n  [1, 2],\n  [3, 4]\n]\n", "server.port"},
		{"path = 'C:\\dir\\' # comment\n", portKey},
		{"banner = \"\"\"\nport = 123\n\"\"\"\nport = 8080\n", portKey},
		{"banner = \"\"\"hello\"\"\" # keep comment\nport = 8080\n", "banner"},
		{"ports = [\n  80,\n  443,\n]\nkeep = true\n", "ports"},
		{"a.b = 1\na.c = 2\n", "a.b"},
		{"Port = 1\nport = 2\n", portKey},
		{"a = 1\n", "a.b"},
		{"x = \"# not a comment\" # real\n", "x"},
		{"", "a.b.c"},
		// Found by fuzzing; each fails on the editor at 949febb.
		{"[a]\nb = '''\n[c]\nd = 1\n'''\n", "c.d"},
		{"server.port = 80\n", "server.host"},
		{"\"\" = 0\n", portKey},
		{"hosts = [\n  [\"a\"]]\n", "debug"},
		{"[\"a,b\"]\nx = 1\n", "debug"},
		{"s = \"\"\"\nsay \\\"\"\"\n[t]\n\"\"\"\nport = 1\n", "t.x"},
		// Found by fuzzing the multi-line-aware line scanner while fixing the above;
		// each broke an intermediate version of it.
		{"[\"\"]\nx = 1\n", "debug"},
		{"s = \"\"\"\n'''\n\"\"\"\n[t]\nx = 1\n", "debug"},
		{"s = \"\"\"\"#\"\"\"\n", "s"},
		{"[0.\"\"]\nx = 1\n", "debug"},
		{"[\" 0\"]\nx = 1\n[\"0\"]\ny = 2\n", "debug"},
	} {
		f.Add(seed.doc, seed.key, int64(42), "", true)
		f.Add(seed.doc, seed.key, int64(0), "new value", false)
	}

	f.Fuzz(func(t *testing.T, doc, key string, n int64, s string, useInt bool) {
		var before map[string]any
		if toml.Unmarshal([]byte(doc), &before) != nil || !bareTOMLKey.MatchString(key) || !utf8.ValidString(s) ||
			multilineInlineTable.MatchString(doc) || mixedLineEndings(doc) || quotedDottedKey.MatchString(doc) {
			t.Skip()
		}

		var value any = s
		if useInt {
			value = n
		}

		path := strings.Split(key, ".")

		out, err := edit.UpdateTOML([]byte(doc), key, value)

		switch tomlPathReach(before, path) {
		case pathThroughScalar:
			if !errors.Is(err, edit.ErrNonObjectNavigation) {
				t.Fatalf("path %q runs through a scalar: err = %v, want ErrNonObjectNavigation\ninput:\n%s\noutput:\n%s", key, err, doc, out)
			}
		case pathThroughArray:
			t.Skip("a path through an array of tables has no single target")
		case leafIsTable:
			if err == nil {
				requireTOMLEdit(t, []byte(doc), out, path, value)
			}
		case pathOK:
			if err != nil {
				t.Fatalf("UpdateTOML(%q) refused a valid edit: %v\ninput:\n%s", key, err, doc)
			}

			requireTOMLEdit(t, []byte(doc), out, path, value)
		}
	})
}

// mixedLineEndings reports a document that uses both CRLF and bare LF, which
// the editor rewrites to CRLF throughout.
func mixedLineEndings(doc string) bool {
	return strings.Contains(doc, "\r\n") && strings.Contains(strings.ReplaceAll(doc, "\r\n", ""), "\n")
}

type pathReach int

const (
	pathOK pathReach = iota
	pathThroughScalar
	pathThroughArray
	leafIsTable
)

// tomlPathReach reports what the parents of path's leaf hold in doc, and
// whether the leaf itself is a table or array of tables, which a scalar may
// not replace.
func tomlPathReach(doc map[string]any, path []string) pathReach {
	cur := doc

	for _, part := range path[:len(path)-1] {
		switch next := cur[part].(type) {
		case nil:
			return pathOK
		case map[string]any:
			cur = next
		case []any:
			return pathThroughArray
		default:
			return pathThroughScalar
		}
	}

	switch cur[path[len(path)-1]].(type) {
	case map[string]any, []any:
		return leafIsTable
	}

	return pathOK
}
