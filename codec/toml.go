package codec

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// TOML implements [Codec] for TOML documents using go-toml/v2.
//
// TOML is stateless and safe for concurrent use.
type TOML struct{}

// NewTOML returns a codec that decodes and encodes TOML documents.
func NewTOML() *TOML {
	return &TOML{}
}

// Decode overlays a TOML document onto target.
//
// A field the document omits keeps the value it already holds. A struct field
// is named by its configuration key, spelled exactly; any other key is ignored
// rather than reported.
//
// It returns [ErrNilTarget] if target is nil. A malformed document returns an
// error wrapping [ErrMalformed] and the go-toml/v2 failure or a [KeyError] for
// each value that does not fit its field.
func (c *TOML) Decode(data []byte, target any) error {
	if isNilTarget(target) {
		return ErrNilTarget
	}

	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%w: toml unmarshal: %w", ErrMalformed, err)
	}

	return decodeOverlay("toml", tomlNode{value: doc}, target)
}

// Encode encodes value as TOML bytes.
//
// Struct fields are written under their configuration keys, so the document
// uses the same keys as every other layer. Plain [time.Duration] values are
// written as strings, such as "30s". The document ends with a newline.
//
// It returns an error wrapping the go-toml/v2 failure if value cannot be
// represented in TOML.
func (c *TOML) Encode(value any) ([]byte, error) {
	keyed, err := keyedTOMLValue(value)
	if err != nil {
		return nil, fmt.Errorf("toml marshal: %w", err)
	}

	data, err := toml.Marshal(keyed)
	if err != nil {
		return nil, fmt.Errorf("toml marshal: %w", err)
	}

	return data, nil
}
