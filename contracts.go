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
// [LoadInto] calls Validate after defaults, files, and environment have been
// merged, and before any CLI overlay. A configuration that a flag would have
// corrected therefore fails validation.
//
// A failure from Validate carries no key. Implement [MetadataValidator] instead
// to attribute a failure to a key and report the file and line it came from.
//
// To validate the fully merged result, including the CLI overlay, load through a
// distinct view type that shares the fields and declares no Validate method, and
// call Validate from the caller.
type Validator interface {
	Validate() error
}

// MetadataValidator allows a configuration struct to report validation failures
// against the keys that caused them.
//
// [LoadInto] prefers MetadataValidator over [Validator] when a type implements
// both, and calls it at the same point in the merge as [Validator.Validate]:
// after defaults, files, and environment, and before any CLI overlay.
//
// A failure built with [Metadata.NewConfigError] carries the key, the raw value
// the configuration supplied, and the layer that supplied it, so the diagnostic
// names the file and line rather than only the problem:
//
//	func (c *Config) ValidateWith(meta *strata.Metadata) error {
//		if c.Port > 9000 {
//			return meta.NewConfigError("port", errors.New("above the privileged ceiling"))
//		}
//
//		return nil
//	}
//
// ValidateWith may return a plain error for a failure that spans several fields
// and has no single key to name, and it may return several attributed failures
// joined with [errors.Join]. A failure that reaches [LoadInto] without an
// attributed key is wrapped in a [ConfigError] with no key.
type MetadataValidator interface {
	ValidateWith(meta *Metadata) error
}
