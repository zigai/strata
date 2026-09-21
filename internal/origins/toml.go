package origins

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// readTOML reports whether data parsed as a non-empty TOML document and, if so,
// emits one Record per leaf key.
//
// Values are rendered with fmt's %v, which is a rendering of the decoded value
// rather than the document's own text.
func readTOML(data []byte, emit func(Record)) bool {
	var document map[string]any

	if err := toml.Unmarshal(data, &document); err != nil || len(document) == 0 {
		return false
	}

	walkMap(document, "", func(key string, value any) {
		emit(Record{Key: key, Line: 0, RawValue: fmt.Sprintf("%v", value)})
	})

	return true
}

// walkMap reports one key per leaf of a decoded document, recursing into nested
// mappings and joining the segments with ".".
func walkMap(document map[string]any, prefix string, emit func(key string, value any)) {
	for key, value := range document {
		fullKey := joinKey(prefix, key)

		if nested, ok := value.(map[string]any); ok {
			walkMap(nested, fullKey, emit)

			continue
		}

		emit(fullKey, value)
	}
}

// joinKey prefixes a dotted key with its parent path.
func joinKey(prefix, key string) string {
	if prefix == "" {
		return key
	}

	return prefix + "." + key
}
