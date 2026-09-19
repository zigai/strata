package codec

import (
	"errors"
	"reflect"
)

// ErrNilTarget is returned by [Codec.Decode] when target is nil.
//
// The codecs in this package return it unwrapped. [errors.Is] and a direct
// comparison both match it.
var ErrNilTarget = errors.New("decode target cannot be nil")

// ErrMalformed is returned by [Codec.Decode] when a document cannot be parsed
// into the target.
//
// Every codec MUST report it, so a caller can tell a broken configuration file
// from a missing one without knowing which format was involved. Classify with
// this error and read the message for the position, which every codec includes.
//
// The parser's own error stays reachable with [errors.As], but its type is not a
// contract. It varies by format and by failure, and for YAML the most common
// failure carries no type at all: the built-in codecs produce a
// [github.com/pelletier/go-toml/v2.DecodeError] for TOML; a
// [gopkg.in/yaml.v3.TypeError] for a YAML type mismatch but a plain error for a
// YAML syntax error; and a *jsontext.SyntacticError or a *json.SemanticError for
// JSON. A codec supplied by a caller may wrap anything.
//
// NB: Reachability is a convenience, not a promise. A caller MUST NOT depend on
// a specific parser type, and this package may change which parser a built-in
// codec wraps.
var ErrMalformed = errors.New("malformed configuration document")

// ErrMultipleDocuments is returned by [YAMLCodec.Decode] when a YAML stream
// carries more than one document. No other codec in this package returns it.
//
// A configuration tier is a single document, and a decoder that honored only
// the first document would discard the rest without reporting it.
//
// The error is wrapped. A caller MUST test for it with [errors.Is].
//
// A stream that is malformed after its first document is not a second document,
// and is reported as [ErrMalformed].
var ErrMultipleDocuments = errors.New("yaml stream contains more than one document")

// Codec decodes one configuration format into a target value and encodes a
// value in that format.
//
// Decode overlays a document onto target. A field the document omits keeps the
// value it already holds, and a key the target does not declare is ignored
// rather than reported. A sparse higher tier can override a single setting
// without erasing the values the lower tiers established.
//
// Implementations MUST be stateless and safe for concurrent use. A single Codec
// value may be held in a [Registry] and called concurrently.
//
// Decode MUST return [ErrNilTarget] when target is nil, and MUST report a
// document it cannot parse by wrapping [ErrMalformed]. A caller of this package
// relies on both to classify a failure without knowing which codec ran.
type Codec interface {
	Decode(data []byte, target any) error
	Encode(value any) ([]byte, error)
}

// isNilTarget reports whether target is nil or a typed nil pointer.
func isNilTarget(target any) bool {
	if target == nil {
		return true
	}

	val := reflect.ValueOf(target)

	return val.Kind() == reflect.Pointer && val.IsNil()
}
