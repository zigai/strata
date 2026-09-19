package edit_test

import (
	"strings"
	"testing"

	"github.com/zigai/strata/internal/edit"
)

func TestUpdateJSON(t *testing.T) {
	t.Parallel()

	input := `{
  "server": {
    "host": "127.0.0.1",
    "port": 8080
  }
}
`

	updated, err := edit.UpdateJSON([]byte(input), "server.port", 9000)
	if err != nil {
		t.Fatalf("UpdateJSON error: %v", err)
	}

	result := string(updated)
	if !strings.Contains(result, "\"port\": 9000") {
		t.Errorf("expected \"port\": 9000 in result, got:\n%s", result)
	}
}

func TestUpdateJSONNullInput(t *testing.T) {
	t.Parallel()

	updated, err := edit.UpdateJSON([]byte("null"), "key", "val")
	if err != nil {
		t.Fatalf("UpdateJSON on null error: %v", err)
	}

	if !strings.Contains(string(updated), "\"key\": \"val\"") {
		t.Errorf("expected key: val in JSON, got:\n%s", string(updated))
	}
}
