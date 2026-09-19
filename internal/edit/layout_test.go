package edit_test

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata/internal/edit"
)

// The editors rewrite presentation, not content. What each one changes is the
// contract SetBytes documents, and it differs enough by format to be worth
// pinning: swapping a parser has already changed observable behavior once, when
// the JSON codec moved from encoding/json to encoding/json/v2.

// TOML is edited line by line. Comments, blank lines, indentation, key order, and
// multi-line values survive. The padding before a trailing comment does not, and
// an appended key is preceded by a blank line.
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

	// Content survives verbatim: the multi-line string and the spread-out array
	// are the shapes a naive re-encode would flatten.
	for _, want := range []string{`"""`, "line one", "  \"a\",\n  \"b\",", "  indented = true"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lost %q:\n%s", want, got)
		}
	}

	// Alignment does not survive.
	if !strings.Contains(got, "port = 9090 # port") {
		t.Errorf("trailing comment padding was not collapsed:\n%s", got)
	}
}

// YAML keeps comments, anchors, aliases, block scalars, flow style, and quoting.
// It drops every blank line, forces two-space indentation, and gives a merge key
// an explicit tag.
func TestUpdateYAMLKeepsContentAndDropsBlankLines(t *testing.T) {
	t.Parallel()

	const document = `# heading
defaults: &d
    timeout: 30
    retries: 3

server:
    port: 8080        # port

    host: "0.0.0.0"
    quoted: 'single'
    flow: {a: 1}
    block: |
      text

production:
    <<: *d
`

	updated, err := edit.UpdateYAML([]byte(document), "server.port", 9090)
	if err != nil {
		t.Fatalf("UpdateYAML: %v", err)
	}

	got := string(updated)

	for _, want := range []string{
		"&d",        // anchor
		"*d",        // alias
		"# heading", // a comment on its own line
		"# port",    // an inline comment
		"'single'",  // single-quoted scalar
		`"0.0.0.0"`, // double-quoted scalar
		"{a: 1}",    // flow style
		"block: |",  // block scalar
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lost %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "\n\n") {
		t.Errorf("blank lines were not dropped:\n%s", got)
	}

	// The document is indented with four spaces, and the edit reproduces that
	// rather than imposing one.
	if !strings.Contains(got, "    port: 9090") {
		t.Errorf("the source indentation was not reproduced:\n%s", got)
	}

	// The merge key is written back as the author wrote it, not with the tag the
	// parser attaches to it.
	if !strings.Contains(got, "<<: *d") {
		t.Errorf("the merge key was rewritten:\n%s", got)
	}

	if strings.Contains(got, "!!merge") {
		t.Errorf("the merge key gained a tag:\n%s", got)
	}

	// The block scalar's body is content. Its indentation moves with the
	// document, so compare what it parses to rather than how it is laid out.
	var before, after struct {
		Block string `yaml:"block"`
	}

	if err := yaml.Unmarshal([]byte(document), &before); err != nil {
		t.Fatalf("parse the input: %v", err)
	}

	if err := yaml.Unmarshal(updated, &after); err != nil {
		t.Fatalf("parse the output: %v", err)
	}

	if before.Block != after.Block {
		t.Errorf("the block scalar changed: %q became %q", before.Block, after.Block)
	}
}

// JSON keeps its indentation and the exact digits of an untouched number, which
// is what stops a large integer being routed through float64. Object keys are
// sorted, and a trailing newline is appended. JSON carries no comments, so there
// is nothing else of the original layout to keep.
func TestUpdateJSONKeepsValuesAndIndentation(t *testing.T) {
	t.Parallel()

	const document = `{
    "server": {
        "port": 8080,
        "host": "0.0.0.0"
    },
    "big": 9007199254740993
}
`

	updated, err := edit.UpdateJSON([]byte(document), "server.port", 9090)
	if err != nil {
		t.Fatalf("UpdateJSON: %v", err)
	}

	got := string(updated)

	if !strings.Contains(got, "9007199254740993") {
		t.Errorf("an untouched number lost its exact digits:\n%s", got)
	}

	if !strings.HasSuffix(got, "\n") {
		t.Errorf("a trailing newline was not added: %q", got)
	}

	// Keys are sorted, so the document order is not the input order.
	if strings.Index(got, `"big"`) > strings.Index(got, `"server"`) {
		t.Errorf("object keys are not sorted:\n%s", got)
	}

	// The document is indented with four spaces, and the edit reproduces it.
	if !strings.Contains(got, "    \"server\": {") {
		t.Errorf("the source indentation was not reproduced:\n%s", got)
	}
}
