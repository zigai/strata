package edit

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
)

// defaultJSONIndent is used when a document is written on one line, so it
// declares no indentation of its own.
const defaultJSONIndent = "  "

// ErrNonObjectNavigation is returned when a dotted path traverses a value that
// is not an object.
//
// The error names the key that blocked navigation.
var ErrNonObjectNavigation = errors.New("cannot navigate through non-object key")

// UpdateJSON returns data with value written at the dotted key, creating
// intermediate objects as needed.
//
// Values the path does not touch are carried through as their original text,
// not decoded and re-encoded; an integer too large for float64 keeps its exact
// value.
//
// The document keeps its original indentation, which is measured from the source.
// Object keys are sorted, and a trailing newline is appended. JSON carries no
// comments, so there is nothing else of the original layout to keep.
//
// A null document is treated as an empty object. It returns
// [ErrNonObjectNavigation] if the path crosses a value that is not an object.
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

// setNested writes value at parts within obj, creating intermediate objects as
// needed.
//
// An intermediate key that already holds a value other than an object blocks
// navigation; that value is not overwritten. It returns
// [ErrNonObjectNavigation] in that case.
func setNested(obj map[string]jsontext.Value, parts []string, value jsontext.Value, indent string) error {
	key := parts[0]

	if len(parts) == 1 {
		obj[key] = value

		return nil
	}

	child := map[string]jsontext.Value{}

	if raw, exists := obj[key]; exists {
		if !isJSONObject(raw) {
			return fmt.Errorf("%w: %q", ErrNonObjectNavigation, key)
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

// jsonSourceIndent reports the indentation step the document was written with, so
// an edit reproduces it rather than imposing one.
//
// A JSON string cannot contain a raw newline, so the leading whitespace of a line
// is always structure rather than content, and the first indented line gives the
// step. A document written on one line reports the default.
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

// isJSONObject reports whether raw holds a JSON object.
//
// Scalars, arrays, and null report false.
func isJSONObject(raw jsontext.Value) bool {
	trimmed := bytes.TrimSpace(raw)

	return len(trimmed) > 0 && trimmed[0] == '{'
}
