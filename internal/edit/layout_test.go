package edit_test

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata/internal/edit"
)

const portKey = "port"

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
		"&d",
		"*d",
		"# heading",
		"# port",
		"'single'",
		`"0.0.0.0"`,
		"{a: 1}",
		"block: |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lost %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "\n\n") {
		t.Errorf("blank lines were not dropped:\n%s", got)
	}

	if !strings.Contains(got, "    port: 9090") {
		t.Errorf("the source indentation was not reproduced:\n%s", got)
	}

	if !strings.Contains(got, "<<: *d") {
		t.Errorf("the merge key was rewritten:\n%s", got)
	}

	if strings.Contains(got, "!!merge") {
		t.Errorf("the merge key gained a tag:\n%s", got)
	}

	// Compare decoded content because scalar indentation may change.
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

	if strings.Index(got, `"big"`) > strings.Index(got, `"server"`) {
		t.Errorf("object keys are not sorted:\n%s", got)
	}

	if !strings.Contains(got, "    \"server\": {") {
		t.Errorf("the source indentation was not reproduced:\n%s", got)
	}
}
