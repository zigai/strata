package codec

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"time"
)

// JSON implements [Codec] for JSON documents using encoding/json/v2.
//
// JSON is stateless and safe for concurrent use.
type JSON struct{}

// NewJSON returns a codec that decodes and encodes JSON documents.
func NewJSON() *JSON {
	return &JSON{}
}

// Decode overlays a JSON document onto target.
//
// Omitted fields keep their values. Only exact configuration keys name fields;
// other members are ignored.
//
// Nil targets return [ErrNilTarget]. Malformed JSON, duplicate members, and
// invalid UTF-8 wrap [ErrMalformed], as does a [KeyError] for each value that
// does not fit its field.
func (c *JSON) Decode(data []byte, target any) error {
	if isNilTarget(target) {
		return ErrNilTarget
	}

	var document jsontext.Value

	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("%w: json unmarshal: %w", ErrMalformed, err)
	}

	start := len(data) - len(bytes.TrimLeft(data, " \t\r\n"))

	return decodeOverlay("json", jsonNode{document: data, offset: int64(start), value: document}, target)
}

// Encode writes value as indented JSON without a trailing newline. Map keys are
// sorted for deterministic output, and struct fields use configuration keys.
func (c *JSON) Encode(value any) ([]byte, error) {
	keyed, err := keyedValue(value, "json")
	if err != nil {
		return nil, fmt.Errorf("json marshal: %w", err)
	}

	data, err := json.Marshal(keyed, jsontext.WithIndent("  "), json.Deterministic(true), json.WithMarshalers(json.MarshalFunc(func(value time.Duration) ([]byte, error) {
		return json.Marshal(value.String())
	})))
	if err != nil {
		return nil, fmt.Errorf("json marshal: %w", err)
	}

	return data, nil
}
