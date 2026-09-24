package codec

import (
	"errors"
	"reflect"
)

// ErrNilTarget is returned by [Codec.Decode] when target is nil.
// Built-in codecs return it unwrapped.
var ErrNilTarget = errors.New("decode target cannot be nil")

// ErrMalformed is wrapped by [Codec.Decode] for malformed documents. Use it to
// classify parse failures across formats. The underlying parser error remains
// available through [errors.As], but its type is not stable.
var ErrMalformed = errors.New("malformed configuration document")

// ErrMultipleDocuments is wrapped by [YAMLCodec.Decode] for a stream with more
// than one document. Malformed trailing content returns [ErrMalformed] instead.
var ErrMultipleDocuments = errors.New("yaml stream contains more than one document")

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

// isNilTarget reports whether target is nil or a typed nil pointer.
func isNilTarget(target any) bool {
	if target == nil {
		return true
	}

	val := reflect.ValueOf(target)

	return val.Kind() == reflect.Pointer && val.IsNil()
}
