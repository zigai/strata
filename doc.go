// Package strata loads configuration from layered sources and records where
// each value came from.
//
// # Layers
//
// Configuration is merged in ascending precedence:
//
//	struct defaults -> system file -> user file -> config file -> environment -> CLI flags
//
// A document overlays the result so far rather than replacing it, so a sparse
// file changes only the keys it mentions. Every layer but the defaults is opt-in:
// [WithAppName] searches the system and user files under the XDG base
// directories and their Windows equivalents, [WithPath] and [WithOptionalPath]
// name one config file, [WithEnvPrefix] binds environment variables, and the CLI
// bridge packages add flags through their WithFlags options.
//
// # Provenance
//
// [LoadWithMetadata] returns a [Metadata] alongside the value. It records, for
// every key that resolved, the layer that supplied it, the file or variable that
// carried it, and the raw text of the value. [Metadata.Where] looks one key up,
// [Metadata.Origins] lists them all, and [Metadata.ActiveFiles] lists the files
// that contributed.
//
// Keys a file sets that the type does not declare are listed by
// [Metadata.UnknownKeys] with the declared key they most resemble, and
// [WithStrict] turns them into errors.
//
// # Extension points
//
// Three interfaces let a configuration struct take part in the load. [Defaulter]
// supplies defaults, [Validator] checks the merged result, and
// [MetadataValidator] checks it while reporting which key failed.
//
// A format is an extension point as well: [WithCodec], [WithDecoder], and
// [WithDecoderFunc] bind a codec or a decode function to a file extension for
// one load.
//
// Writing is not extensible that way. [Save], [Init], and [Set] serve the
// built-in TOML, YAML, and JSON formats only: [Set] edits a document's layout
// rather than re-encoding it, and [Init] writes a format-specific schema header.
//
// # Errors
//
// Every error this package raises for a condition it names is classifiable
// through a sentinel declared in it, so a caller never imports a subsystem to
// branch on a failure. A failure it does not name is passed through as raised:
// a path that cannot be opened reports an [*fs.PathError], and a document the
// editors cannot parse reports the parser's own error.
//
// A sentinel is declared in the package that raises the condition, and this
// package re-exports it when the condition can reach a caller. The re-export is
// an alias rather than a copy, so both names match the same value and
// [errors.Is] succeeds with either:
//
//	var ErrFileTooLarge = stream.ErrFileTooLarge
//
// [ErrReflectSchema], [ErrFileExists], [ErrUnsupportedFormat], and
// [ErrEmptyDuration] are declared rather than re-exported, because this package
// raises them itself.
//
// # Layout
//
// This package is the facade. The github.com/zigai/strata/codec package defines
// the format SPI, and the CLI bridges live in their own modules so that this
// package depends on no CLI framework.
package strata
