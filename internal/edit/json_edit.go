package edit

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
)

const defaultJSONIndent = "  "

var ErrPathNotMapping = errors.New("cannot navigate through non-object key")

func UpdateJSON(data []byte, dottedKey string, value any) ([]byte, error) {
	var root map[string]jsontext.Value

	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	if root == nil {
		root = make(map[string]jsontext.Value)
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal value: %w", err)
	}

	indent := jsonSourceIndent(data)

	if err := setNested(root, strings.Split(dottedKey, "."), jsontext.Value(encoded), indent); err != nil {
		return nil, err
	}

	updated, err := json.Marshal(root, jsontext.WithIndent(indent), json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("marshal updated json: %w", err)
	}

	return append(updated, '\n'), nil
}

func setNested(obj map[string]jsontext.Value, parts []string, value jsontext.Value, indent string) error {
	key := parts[0]

	if len(parts) == 1 {
		obj[key] = value

		return nil
	}

	child := map[string]jsontext.Value{}

	if raw, exists := obj[key]; exists {
		if !isJSONObject(raw) {
			return fmt.Errorf("%w: %q", ErrPathNotMapping, key)
		}

		if err := json.Unmarshal(raw, &child); err != nil {
			return fmt.Errorf("parse nested object %q: %w", key, err)
		}

		if child == nil {
			child = map[string]jsontext.Value{}
		}
	}

	if err := setNested(child, parts[1:], value, indent); err != nil {
		return err
	}

	encoded, err := json.Marshal(child, jsontext.WithIndent(indent), json.Deterministic(true))
	if err != nil {
		return fmt.Errorf("marshal nested object %q: %w", key, err)
	}

	obj[key] = jsontext.Value(encoded)

	return nil
}

// A JSON string cannot contain a raw newline, so leading whitespace on a line
// is structure rather than content.
func jsonSourceIndent(data []byte) string {
	for line := range bytes.Lines(data) {
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) == 0 || len(trimmed) == len(line) {
			continue
		}

		return string(line[:len(line)-len(trimmed)])
	}

	return defaultJSONIndent
}

func isJSONObject(raw jsontext.Value) bool {
	trimmed := bytes.TrimSpace(raw)

	return len(trimmed) > 0 && trimmed[0] == '{'
}
