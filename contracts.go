package strata

import (
	"github.com/zigai/strata/internal/defaulter"
)

// Defaulter allows a configuration struct to declare its defaults in Go instead
// of in a struct tag.
//
// [LoadInto] calls SetDefaults on the target, and on every nested struct that
// implements Defaulter, before reading any configuration layer. SetDefaults MUST
// be idempotent; it runs once per [Load] or [LoadInto] call.
//
// A panic from SetDefaults is recovered and returned as an error wrapping
// [ErrSetDefaultsPanicked] rather than being allowed to escape. A promoted
// method on a nil embedded pointer therefore fails the load instead of the
// process.
type Defaulter = defaulter.Defaulter

// Validator allows a configuration struct to check its own invariants.
//
// [LoadInto] calls Validate after all layers and contributions have merged.
//
// Use [MetadataValidator] to attribute a failure to a key and its source.
type Validator interface {
	Validate() error
}

// MetadataValidator allows a configuration struct to report validation failures
// against the keys that caused them.
//
// [LoadInto] calls it after the merge, preferring it over [Validator] when both
// are implemented.
//
// ValidateWith can return plain or attributed errors, including multiple errors
// joined with [errors.Join]. Unattributed failures have no key in [ConfigError].
type MetadataValidator interface {
	ValidateWith(meta *Metadata) error
}
