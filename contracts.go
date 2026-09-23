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
// [LoadInto] calls Validate after defaults, files, environment, and all
// contributions registered through [WithContribution] have merged. A CLI flag
// contributed before validation can therefore satisfy or correct an invariant
// that lower layers did not meet.
//
// A failure from Validate carries no key. Implement [MetadataValidator] instead
// to attribute a failure to a key and report the file and line it came from.
type Validator interface {
	Validate() error
}

// MetadataValidator allows a configuration struct to report validation failures
// against the keys that caused them.
//
// [LoadInto] prefers MetadataValidator over [Validator] when a type implements
// both, and calls it at the same point in the merge as [Validator.Validate]:
// after defaults, files, environment, and any contributions registered through
// [WithContribution] have merged.
//
// ValidateWith may return a plain error for a failure that spans several fields
// and has no single key to name, and it may return several attributed failures
// joined with [errors.Join]. A failure that reaches [LoadInto] without an
// attributed key is wrapped in a [ConfigError] with no key.
type MetadataValidator interface {
	ValidateWith(meta *Metadata) error
}
