package strata

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/zigai/strata/internal/keypath"

	"github.com/zigai/strata/codec"
)

var errRedactedValue = errors.New("invalid value [REDACTED]")

// Secret is a string value that redacts itself when formatted or logged.
// Use Value to retrieve the underlying string. Save writes the real value;
// Init omits the field from generated templates.
type Secret string

type secretValueError struct {
	cause error
}

// decodeError describes a decoding failure without its secret values. It
// matches the sentinels of its cause but does not unwrap to it, because the
// cause quotes the values.
type decodeError struct {
	message string
	cause   error
}

// String returns the redacted form of the secret.
func (s Secret) String() string { return "[REDACTED]" }

// GoString returns the redacted form used by %#v.
func (s Secret) GoString() string { return "[REDACTED]" }

// LogValue returns a redacted slog value.
func (s Secret) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

// Value returns the underlying secret string.
func (s Secret) Value() string { return string(s) }

func (err *secretValueError) Error() string { return "invalid configuration value [REDACTED]" }

func (err *secretValueError) GoString() string { return err.Error() }

func (err *secretValueError) Is(target error) bool { return errors.Is(err.cause, target) }

func (err *decodeError) Error() string { return err.message }

func (err *decodeError) Is(target error) bool { return errors.Is(err.cause, target) }

// redactDecodeError keeps the failure of every key that is not secret and
// redacts the value of every key that is. When any part of err cannot be
// attributed to a key, the whole failure is redacted.
func redactDecodeError(err error, keys *keyTree) error {
	failures, attributed := keyErrors(err)
	if !attributed || len(failures) == 0 {
		return &secretValueError{cause: err}
	}

	parts := make([]string, len(failures))
	for index, failure := range failures {
		parts[index] = failure.Error()

		if secretFailure(keys, failure.Key) {
			redacted := codec.KeyError{Key: failure.Key, Line: failure.Line, Err: errRedactedValue}
			parts[index] = redacted.Error()
		}
	}

	return &decodeError{message: codec.ErrMalformed.Error() + ": " + strings.Join(parts, "\n"), cause: err}
}

func secretFailure(keys *keyTree, dotted string) bool {
	key, err := keypath.Parse(dotted)
	if err != nil {
		// An empty key is the document itself, or an empty key at its top
		// level, so it is as secret as anything in the document.
		return keys.secret("")
	}

	return keys.secret(key)
}

// keyErrors lists the key failures in err and reports whether they are all of
// it: wrapping adds only fixed context, and ErrMalformed is a classification.
func keyErrors(err error) ([]*codec.KeyError, bool) {
	// A KeyError found through a wrapper whose message is its own is the
	// failure itself; extra wrapping text is left out of the description.
	if failure, ok := errors.AsType[*codec.KeyError](err); ok && failure.Error() == err.Error() {
		return []*codec.KeyError{failure}, true
	}

	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var failures []*codec.KeyError

		for _, child := range joined.Unwrap() {
			found, attributed := keyErrors(child)
			if !attributed {
				return nil, false
			}

			failures = append(failures, found...)
		}

		return failures, true
	}

	if inner := errors.Unwrap(err); inner != nil {
		return keyErrors(inner)
	}

	// The only other part a built-in codec adds is the ErrMalformed sentinel.
	return nil, errors.Is(err, codec.ErrMalformed)
}
