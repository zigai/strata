package origins

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// readJSON emits one Record per leaf key of a JSON document.
//
// It reports nothing, unlike the TOML and YAML readers: JSON is the last resort
// of [Read], so whether it parsed is not consulted.
//
// Each value is kept as its own JSON text rather than decoded into any, because
// decoding a number into any routes it through float64 and would render
// 9007199254740993 as 9.007199254740992e+15. The bytes of the document are
// already the exact text, so they are read directly.
func readJSON(data []byte, emit func(Record)) {
	var document map[string]jsontext.Value

	if err := json.Unmarshal(data, &document); err != nil {
		return
	}

	walkJSONObject(document, "", emit)
}

// walkJSONObject emits one Record per leaf of a JSON object, recursing into
// nested objects.
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

		emit(Record{Key: fullKey, Line: 0, RawValue: renderJSONValue(raw)})
	}
}

// renderJSONValue reports a JSON value the way the other readers report theirs:
// a string without its quotes, and anything else as its literal text.
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

// isJSONObject reports whether raw holds a JSON object, as opposed to a scalar,
// an array, or null.
func isJSONObject(raw jsontext.Value) bool {
	trimmed := bytes.TrimSpace(raw)

	return len(trimmed) > 0 && trimmed[0] == '{'
}
