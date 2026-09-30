package origins

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// TOML values are rendered from decoded values, rather than the original text.
func readTOML(data []byte, emit func(Record)) bool {
	var document map[string]any

	if err := toml.Unmarshal(data, &document); err != nil || len(document) == 0 {
		return false
	}

	walkMap(document, "", func(key string, value any) {
		emit(Record{Key: key, Line: 0, RawValue: fmt.Sprintf("%v", value), IsTemplate: false})
	})

	return true
}

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

func joinKey(prefix, key string) string {
	if prefix == "" {
		return key
	}

	return prefix + "." + key
}
