package origins

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// Decoding a number into any routes it through float64 and loses digits.
// Read the original JSON text directly to preserve values such as 9007199254740993.
func readJSON(data []byte, emit func(Record)) {
	var document map[string]jsontext.Value

	if err := json.Unmarshal(data, &document); err != nil {
		return
	}

	walkJSONObject(document, "", emit)
}

func walkJSONObject(document map[string]jsontext.Value, prefix string, emit func(Record)) {
	for key, raw := range document {
		fullKey := joinKey(prefix, key)

		if isJSONObject(raw) {
			var nested map[string]jsontext.Value

			if err := json.Unmarshal(raw, &nested); err == nil {
				walkJSONObject(nested, fullKey, emit)

				continue
			}
		}

		emit(Record{Key: fullKey, Line: 0, RawValue: renderJSONValue(raw), IsTemplate: false})
	}
}

func renderJSONValue(raw jsontext.Value) string {
	trimmed := bytes.TrimSpace(raw)

	if len(trimmed) > 0 && trimmed[0] == '"' {
		var value string

		if err := json.Unmarshal(trimmed, &value); err == nil {
			return value
		}
	}

	return string(trimmed)
}

func isJSONObject(raw jsontext.Value) bool {
	trimmed := bytes.TrimSpace(raw)

	return len(trimmed) > 0 && trimmed[0] == '{'
}
