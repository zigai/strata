package codec

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/zigai/strata/internal/layout"
)

// ErrNilTarget is returned by [Codec.Decode] when target is nil.
// Built-in codecs return it unwrapped.
var ErrNilTarget = errors.New("decode target cannot be nil")

// ErrMalformed is wrapped by [Codec.Decode] for malformed documents. Use it to
// classify parse failures across formats. The underlying parser error remains
// available through [errors.As], but its type is not stable.
var ErrMalformed = errors.New("malformed configuration document")

// ErrMultipleDocuments is wrapped by [YAML.Decode] for a stream with more
// than one document. Malformed trailing content returns [ErrMalformed] instead.
var ErrMultipleDocuments = errors.New("yaml stream contains more than one document")

// ErrInvalidLayout is wrapped by [Codec.Decode] and [Codec.Encode] when the
// target type declares keys that cannot be laid out, such as an inline field
// that is neither a struct nor a map with string keys, more than one inline
// map, or two fields that claim the same key at the same depth.
var ErrInvalidLayout = layout.ErrInvalid

// Codec decodes and encodes one configuration format.
//
// Decode overlays target: omitted fields keep their values, and unknown keys
// are ignored.
//
// Implementations must be stateless and safe for concurrent use.
//
// Decode must return [ErrNilTarget] for nil targets and wrap [ErrMalformed] for
// malformed documents.
type Codec interface {
	Decode(data []byte, target any) error
	Encode(value any) ([]byte, error)
}

// KeyError reports a value that could not be decoded into the field a
// configuration key names. Built-in codecs wrap each such failure in one. Key
// is the dotted configuration key, such as "db.port", with a literal dot or
// backslash in a key escaped as \. or \\; it is empty for the document
// itself. Line is the 1-based line when the format reports one, otherwise 0.
type KeyError struct {
	Key  string
	Line int
	Err  error
}

// Error names the key and line, then the decoding failure.
func (e *KeyError) Error() string {
	var b strings.Builder

	if e.Line > 0 {
		fmt.Fprintf(&b, "line %d: ", e.Line)
	}

	if e.Key != "" {
		fmt.Fprintf(&b, `key "%s": `, e.Key)
	}

	b.WriteString(e.Err.Error())

	return b.String()
}

// Unwrap returns the decoding failure.
func (e *KeyError) Unwrap() error { return e.Err }

func isNilTarget(target any) bool {
	if target == nil {
		return true
	}

	val := reflect.ValueOf(target)

	return val.Kind() == reflect.Pointer && val.IsNil()
}
