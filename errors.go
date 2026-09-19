package strata

import (
	"errors"

	"github.com/zigai/strata/codec"
	"github.com/zigai/strata/internal/cascade"
	"github.com/zigai/strata/internal/defaulter"
	"github.com/zigai/strata/internal/edit"
	"github.com/zigai/strata/internal/env"
	"github.com/zigai/strata/internal/stream"
)

var (
	// ErrNoCodec is returned when no codec can be selected for a configuration
	// layer. The layer's extension is consulted first, and its contents after
	// that.
	ErrNoCodec = errors.New("no suitable codec found for configuration format")

	// ErrCodecTargetMismatch is returned when a typed decoder receives a target
	// value whose type does not match the decoder's expected type parameter.
	ErrCodecTargetMismatch = errors.New("codec target type mismatch")

	// ErrTargetNotPointer is returned when [LoadInto] is given a nil pointer, or
	// a pointer whose element is not a struct.
	ErrTargetNotPointer = defaulter.ErrTargetNotPointer

	// ErrSetDefaultsPanicked is returned when a [Defaulter]'s SetDefaults panics.
	// The panicking value is included in the message.
	ErrSetDefaultsPanicked = defaulter.ErrSetDefaultsPanicked

	// ErrFileTooLarge is returned when one configuration file or stream exceeds
	// the byte limit set by [WithMaxFileSize]. Input is never truncated.
	ErrFileTooLarge = stream.ErrFileTooLarge

	// ErrPathIsDirectory is returned when a configuration path that must name a
	// file names a directory.
	ErrPathIsDirectory = cascade.ErrPathIsDirectory

	// ErrInvalidEnvValue is returned when a bound environment variable cannot be
	// decoded into its field. It covers a value too wide for the field's type,
	// and a field type that cannot be bound from the environment at all.
	ErrInvalidEnvValue = env.ErrInvalidEnvValue

	// ErrMalformed is returned when a configuration file cannot be parsed into
	// the target. It covers every format, so a caller can tell a broken file
	// from a missing one without knowing which codec ran.
	//
	// The error also wraps the parser's own error, reachable with [errors.As].
	ErrMalformed = codec.ErrMalformed

	// ErrMultipleDocuments is returned for a YAML stream carrying more than one
	// document. A configuration tier is a single document.
	//
	// Both [Load] and [SetBytes] report it, and both report the same value, so
	// one check covers a document that cannot be read and one that cannot be
	// rewritten.
	ErrMultipleDocuments = codec.ErrMultipleDocuments

	// ErrNonObjectNavigation is returned when [SetBytes] must traverse a value
	// that is not an object to reach the requested key.
	ErrNonObjectNavigation = edit.ErrNonObjectNavigation

	// ErrRootNotMapping is returned when [SetBytes] is given a document whose
	// root is not a mapping, so there is no key to address.
	ErrRootNotMapping = edit.ErrRootNotMapping

	// ErrEmptyEncodedValue is returned when the value passed to [SetBytes]
	// encodes to an empty document, leaving the key with nothing to hold.
	ErrEmptyEncodedValue = edit.ErrEmptyEncodedValue
)
