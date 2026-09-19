package codec

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// TOMLCodec implements [Codec] for TOML documents using go-toml/v2.
//
// A TOMLCodec is stateless and safe for concurrent use.
type TOMLCodec struct{}

// NewTOMLCodec returns a codec that decodes and encodes TOML documents.
func NewTOMLCodec() *TOMLCodec {
	return &TOMLCodec{}
}

// Decode overlays a TOML document onto target.
//
// A field the document omits keeps the value it already holds, and a key the
// target does not declare is ignored rather than reported.
//
// It returns [ErrNilTarget] if target is nil. A malformed document returns an
// error wrapping the go-toml/v2 failure.
func (c *TOMLCodec) Decode(data []byte, target any) error {
	if isNilTarget(target) {
		return ErrNilTarget
	}

	if err := toml.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%w: toml unmarshal: %w", ErrMalformed, err)
	}

	return nil
}

// Encode encodes value as TOML bytes.
//
// The document is terminated by a newline.
//
// It returns an error wrapping the go-toml/v2 failure if value cannot be
// represented in TOML.
func (c *TOMLCodec) Encode(value any) ([]byte, error) {
	data, err := toml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("toml marshal: %w", err)
	}

	return data, nil
}
