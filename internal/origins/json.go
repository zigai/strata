package origins

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strconv"
)

// Decoding a number into any routes it through float64 and loses digits.
// Read the original JSON text directly to preserve values such as 9007199254740993.
func readJSON(data []byte, emit func(Record)) bool {
	var document map[string]jsontext.Value

	if err := json.Unmarshal(data, &document); err != nil {
		return false
	}

	walkJSONObject(document, "", emit)

	return true
}

func walkJSONObject(document map[string]jsontext.Value, prefix jsontext.Pointer, emit func(Record)) {
	for _, key := range slices.Sorted(maps.Keys(document)) {
		walkJSONValue(document[key], prefix.AppendToken(key), emit)
	}
}

func walkJSONValue(raw jsontext.Value, key jsontext.Pointer, emit func(Record)) {
	if isJSONObject(raw) {
		var nested map[string]jsontext.Value
		if err := json.Unmarshal(raw, &nested); err == nil && len(nested) > 0 {
			walkJSONObject(nested, key, emit)
			return
		}
	}

	emit(Record{Key: key, Line: 0, RawValue: renderJSONValue(raw), IsTemplate: false})

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return
	}

	var items []jsontext.Value
	if err := json.Unmarshal(raw, &items); err != nil {
		return
	}

	for index, item := range items {
		item = bytes.TrimSpace(item)
		if len(item) > 0 && (item[0] == '{' || item[0] == '[') {
			walkJSONValue(item, key.AppendToken(strconv.Itoa(index)), emit)
		}
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
