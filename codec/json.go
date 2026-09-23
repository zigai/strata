package codec

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
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
// A field the document omits keeps the value it already holds, and a key the
// target does not declare is ignored rather than reported.
//
// A member names a field by the name encoding/json matches — the field's json
// tag, or its Go name when the tag does not name one — and by the configuration
// key the rest of the package derives for it: the first of its strata, toml,
// yaml, and json tags that names the field, or the snake_case form of its Go
// name. A field with no tag is therefore reachable both by its Go name and by
// its snake_case form, which is the key provenance reports and the environment
// tier binds. Two members that name one field are refused rather than resolved
// by picking one.
//
// It returns [ErrNilTarget] if target is nil. A malformed document is an error,
// as are duplicate member names and invalid UTF-8; v2 rejects both by default.
// TOML and YAML reject the same two conditions, and all three codecs agree.
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

	if err := json.Unmarshal(document, target); err != nil {
		return fmt.Errorf("%w: json unmarshal: %w", ErrMalformed, err)
	}

	return nil
}

// Encode encodes value as JSON bytes indented with two spaces.
//
// Output is deterministic: object keys of maps are emitted in sorted order, and
// the same value always encodes to the same bytes. Determinism is requested
// explicitly because v2 randomizes map iteration order by default, unlike the
// v1 encoder this replaced.
//
// Unlike the TOML and YAML encoders, the result is not terminated by a newline.
//
// Struct fields are written under their configuration keys, the names strata
// resolves from the strata, toml, yaml, and json tags or the snake_case field
// name, so the document uses the same keys as every other layer.
//
// It returns an error wrapping the v2 failure if value cannot be represented in
// JSON.
func (c *JSONCodec) Encode(value any) ([]byte, error) {
	data, err := json.Marshal(keyedValue(value), jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("json marshal: %w", err)
	}

	return data, nil
}
