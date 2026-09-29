package edit_test

import (
	"strings"
	"testing"

	"github.com/zigai/strata/internal/edit"
)

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
