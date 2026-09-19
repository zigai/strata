package codec

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// YAMLCodec implements [Codec] for YAML documents using yaml.v3.
//
// A YAMLCodec is stateless and safe for concurrent use.
type YAMLCodec struct{}

// NewYAMLCodec returns a codec that decodes and encodes YAML documents.
func NewYAMLCodec() *YAMLCodec {
	return &YAMLCodec{}
}

// Decode overlays a YAML document onto target.
//
// A field the document omits keeps the value it already holds, and a key the
// target does not declare is ignored rather than reported.
//
// It returns [ErrNilTarget] if target is nil. A malformed document returns an
// error wrapping the yaml.v3 failure, and a stream carrying more than one
// document returns [ErrMultipleDocuments]. An empty document is not an error.
func (c *YAMLCodec) Decode(data []byte, target any) error {
	if isNilTarget(target) {
		return ErrNilTarget
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))

	if err := decoder.Decode(target); err != nil {
		// An empty document has nothing to overlay and is not an error.
		if errors.Is(err, io.EOF) {
			return nil
		}

		return fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
	}

	var extra yaml.Node

	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
		// Exactly one document, which is what a configuration tier must be.
	case err != nil:
		// The stream is malformed after the first document. That is a parse
		// failure, not a second document.
		return fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
	default:
		return fmt.Errorf("yaml unmarshal: %w", ErrMultipleDocuments)
	}

	return nil
}

// Encode encodes value as YAML bytes.
//
// The document is terminated by a newline.
//
// It returns an error wrapping the yaml.v3 failure if value cannot be
// represented in YAML.
func (c *YAMLCodec) Encode(value any) ([]byte, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("yaml marshal: %w", err)
	}

	return data, nil
}
