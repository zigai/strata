package edit_test

import (
	"errors"
	"io"
	"maps"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata/internal/edit"
)

func requireYAMLEdit(t *testing.T, input, output []byte, path []string, value any) {
	t.Helper()

	before, ok := decodeYAMLDocument(input)
	if !ok {
		t.Fatalf("input does not decode to a string-keyed mapping:\n%s", input)
	}

	after, ok := decodeYAMLDocument(output)
	if !ok {
		t.Fatalf("output does not decode to a string-keyed mapping:\ninput:\n%s\noutput:\n%s", input, output)
	}

	want := setYAMLDecoded(before, path, decodedYAMLValue(t, value))
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("edit of %q changed the document wrongly\ninput:\n%s\noutput:\n%s\ndecoded: %#v\nwant:    %#v",
			strings.Join(path, "."), input, output, after, want)
	}
}

func decodeYAMLDocument(data []byte) (map[string]any, bool) {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false
	}

	// A document with no node at all is an empty mapping; a null or tagged
	// empty root is a value that is not a mapping.
	if doc == nil {
		var node yaml.Node
		if yaml.Unmarshal(data, &node) != nil || len(node.Content) != 0 {
			return nil, false
		}

		return map[string]any{}, true
	}

	root, ok := doc.(map[string]any)

	return root, ok && stringKeyed(root)
}

func stringKeyed(v any) bool {
	switch v := v.(type) {
	case map[string]any:
		for _, child := range v {
			if !stringKeyed(child) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !stringKeyed(child) {
				return false
			}
		}
	case map[any]any:
		return false
	}

	return true
}

// Values are derived by hand: yaml.v3 drops a leading newline on its own round trip.
func decodedYAMLValue(t *testing.T, value any) any {
	t.Helper()

	switch v := value.(type) {
	case string:
		return v
	case int:
		return v
	case int64:
		return int(v)
	default:
		t.Fatalf("no reference decoding for %T", value)

		return nil
	}
}

func setYAMLDecoded(doc map[string]any, path []string, value any) map[string]any {
	out := make(map[string]any, len(doc)+1)
	maps.Copy(out, doc)

	if len(path) == 1 {
		out[path[0]] = value

		return out
	}

	child, _ := out[path[0]].(map[string]any)
	out[path[0]] = setYAMLDecoded(child, path[1:], value)

	return out
}

func TestUpdateYAMLChangesOnlyTheNamedKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		input string
		key   string
		value any
	}{
		{"comments", "# main\nserver:\n  # host\n  host: 127.0.0.1 # inline\n  port: 8080\n\ndatabase:\n  port: 5432\n", "server.port", 9090},
		{"append in mapping", "server:\n  port: 8080\n", "server.timeout", "30s"},
		{"key with siblings after it", "server:\n  port: 8080\n  host: h\n  tls: true\nname: app\n", "server.port", 9090},
		{"through an alias", "default: &default\n  timeout: 30s\n  retries: 3\n\nproduction: *default\n", "production.retries", 5},
		{"deep through an alias", "default: &default\n  nested:\n    val: 10\nserver: *default\n", "server.nested.val", 999},
		{"case-different sibling", "Port: 8080\nport: 9090\n", portKey, 9999},
		{"merge key override", "base: &base\n  host: a\n  port: 1\ndb:\n  <<: *base\n", "db.port", 2},
		{"nested mapping from the first merge source", "first: &f {n: {x: 1}}\nsecond: &s {n: {x: 2, y: 3}}\nc:\n  <<: [*f, *s]\n", "c.n.z", 4},
		{"second alias of an edited alias", "a: &x\n  p: 1\nb: *x\nc: *x\n", "b.p", 2},
		{"four-space indent", "server:\n    host: h\n    port: 1\n", "server.port", 2},
		{"flow mapping", "server: {host: h, port: 1}\n", "server.port", 2},
		{"block scalar sibling", "motd: |\n  line one\n  line two\nport: 1\n", portKey, 2},
		{"empty document", "", "a.b", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := edit.UpdateYAML([]byte(tc.input), tc.key, tc.value)
			if err != nil {
				t.Fatalf("UpdateYAML: %v", err)
			}

			requireYAMLEdit(t, []byte(tc.input), out, strings.Split(tc.key, "."), tc.value)
		})
	}
}

func TestUpdateYAMLReplacedAnchorKeepsItsAliases(t *testing.T) {
	t.Parallel()

	out, err := edit.UpdateYAML([]byte("a: &v 1\nb: *v\n"), "a.x", 2)
	if err != nil {
		t.Fatalf("UpdateYAML: %v", err)
	}

	got, ok := decodeYAMLDocument(out)
	if !ok {
		t.Fatalf("output does not decode to a string-keyed mapping:\n%s", out)
	}

	want := map[string]any{"a": map[string]any{"x": 2}, "b": map[string]any{"x": 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded %#v, want %#v\noutput:\n%s", got, want, out)
	}
}

var bareYAMLKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z_][A-Za-z0-9_-]*)*$`)

// Editing an anchored node changes its aliases too, so shared-node cases
// are excluded from the property that only the requested key changes.
func FuzzUpdateYAML(f *testing.F) {
	for _, seed := range []struct {
		doc, key string
	}{
		{"# main\nserver:\n  # host\n  host: 127.0.0.1 # inline\n  port: 8080\n\ndatabase:\n  port: 5432\n", "server.port"},
		{"default: &default\n  timeout: 30s\n  retries: 3\n\nproduction: *default\n", "production.retries"},
		{"default: &default\n  nested:\n    val: 10\nserver: *default\n", "server.nested.val"},
		{"base: &base\n  host: a\n  port: 1\ndb:\n  <<: *base\n", "db.port"},
		{"base: &base\n  nested:\n    x: 1\n    y: 2\ndb:\n  <<: *base\n", "db.nested.x"},
		{"first: &f {a: 1}\nsecond: &s {b: 2}\nc:\n  <<: [*f, *s]\n", "c.b"},
		{"server:\n    host: h\n    port: 1\n", "server.port"},
		{"server: {host: h, port: 1}\n", "server.port"},
		{"motd: |\n  line one\n  line two\nport: 1\n", portKey},
		{"list:\n  - a\n  - b\n", "list.x"},
		{"a: 1\n", "a.b"},
		{"Port: 1\nport: 2\n", portKey},
		{"", "a.b"},
		{"a: &x\n  p: 1\nb: *x\nc: *x\n", "b.p"},
		// Found by fuzzing; each fails on the editor at 949febb.
		{"server: {timeout: , port: 1}\n", portKey},
		{"motd: |\n\n  Welcome\n  to the server\nport: 1\n", portKey},
		{"about: >\n  Folded text\n    indented line\nport: 1\n", portKey},
	} {
		f.Add(seed.doc, seed.key, int64(42), "", true)
		f.Add(seed.doc, seed.key, int64(0), "new value", false)
	}

	// A new value that starts with a newline must keep it.
	f.Add("port: 1\n", "motd", int64(0), "\nWelcome", false)

	f.Fuzz(func(t *testing.T, doc, key string, n int64, s string, useInt bool) {
		_, ok := decodeYAMLDocument([]byte(doc))
		if !ok || !bareYAMLKey.MatchString(key) || !utf8.ValidString(s) || !singleYAMLDocument(doc) {
			t.Skip()
		}

		var value any = s
		if useInt {
			value = n
		}

		path := strings.Split(key, ".")
		if yamlEditShared([]byte(doc), path) {
			t.Skip("the edit reaches a node other keys alias")
		}

		out, err := edit.UpdateYAML([]byte(doc), key, value)
		if err != nil {
			t.Fatalf("UpdateYAML(%q) refused a valid edit: %v\ninput:\n%s", key, err, doc)
		}

		requireYAMLEdit(t, []byte(doc), out, path, value)
	})
}

func singleYAMLDocument(doc string) bool {
	dec := yaml.NewDecoder(strings.NewReader(doc))

	var first, second yaml.Node
	if dec.Decode(&first) != nil {
		return doc == "" || strings.TrimSpace(doc) == ""
	}

	return errors.Is(dec.Decode(&second), io.EOF)
}

// yamlEditShared reports whether editing path in doc also changes a value other
// keys alias. It walks the node tree the way an in-place edit would: a node
// reached directly is edited in place, so the edit is shared when such a node,
// or the leaf being replaced, is the target of an alias. A step through an alias
// or a merge edits a copy and shares nothing past it.
func yamlEditShared(data []byte, path []string) bool {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) == 0 {
		return false
	}

	targets := map[*yaml.Node]bool{}
	collectAliasTargets(&doc, targets)

	node := doc.Content[0]

	for i, part := range path {
		var value *yaml.Node

		for j := 0; j+1 < len(node.Content); j += 2 {
			if node.Content[j].Value == part {
				value = node.Content[j+1]

				break
			}
		}

		switch {
		case value == nil, value.Kind == yaml.AliasNode:
			return false
		case targets[value]:
			return true
		case i == len(path)-1 || value.Kind != yaml.MappingNode:
			return false
		}

		node = value
	}

	return false
}

func collectAliasTargets(node *yaml.Node, targets map[*yaml.Node]bool) {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		targets[node.Alias] = true
	}

	for _, child := range node.Content {
		collectAliasTargets(child, targets)
	}
}
