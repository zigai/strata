package codec

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"time"
)

// JSONCodec implements [Codec] for JSON documents using encoding/json/v2.
//
// A JSONCodec is stateless and safe for concurrent use.
type JSONCodec struct{}

// NewJSONCodec returns a codec that decodes and encodes JSON documents.
func NewJSONCodec() *JSONCodec {
	return &JSONCodec{}
}

// Decode overlays a JSON document onto target.
//
// Omitted fields keep their values. Only exact configuration keys name fields;
// other members are ignored.
//
// Nil targets return [ErrNilTarget]. Malformed JSON, duplicate members, and
// invalid UTF-8 wrap [ErrMalformed].
func (c *JSONCodec) Decode(data []byte, target any) error {
	if isNilTarget(target) {
		return ErrNilTarget
	}

	var document jsontext.Value

	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("%w: json unmarshal: %w", ErrMalformed, err)
	}

	rewritten, changed, err := rewriteJSONDocument(document, target)
	if err != nil {
		return err
	}

	if changed {
		document = rewritten
	}

	if err := json.Unmarshal(document, target, json.WithUnmarshalers(json.UnmarshalFunc(func(data []byte, value *time.Duration) error {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("decode duration string: %w", err)
		}

		duration, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("parse duration: %w", err)
		}

		*value = duration

		return nil
	}))); err != nil {
		return fmt.Errorf("%w: json unmarshal: %w", ErrMalformed, err)
	}

	return nil
}

// Encode writes value as indented JSON without a trailing newline. Map keys are
// sorted for deterministic output, and struct fields use configuration keys.
func (c *JSONCodec) Encode(value any) ([]byte, error) {
	data, err := json.Marshal(keyedValue(value), jsontext.WithIndent("  "), json.Deterministic(true), json.WithMarshalers(json.MarshalFunc(func(value time.Duration) ([]byte, error) {
		return json.Marshal(value.String())
	})))
	if err != nil {
		return nil, fmt.Errorf("json marshal: %w", err)
	}

	return data, nil
}
