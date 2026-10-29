package edit_test

import (
	"strings"
	"testing"

	"github.com/zigai/strata/internal/edit"
)

func TestUpdateJSONNullInput(t *testing.T) {
	t.Parallel()

	updated, err := edit.UpdateJSON([]byte("null"), []string{"key"}, "val")
	if err != nil {
		t.Fatalf("UpdateJSON on null error: %v", err)
	}

	if !strings.Contains(string(updated), "\"key\": \"val\"") {
		t.Errorf("expected key: val in JSON, got:\n%s", string(updated))
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

	updated, err := edit.UpdateJSON([]byte(document), []string{"server", "port"}, 9090)
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
