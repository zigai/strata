// Package strata loads configuration from layered sources and records where
// each value came from.
//
// # Layers
//
// Configuration is read from five sources and merged in ascending precedence:
//
//	struct defaults -> system file -> user file -> project file -> environment
//
// The CLI bridge packages apply a sixth layer, the command line, from the
// caller's side after Load returns. A document overlays the result so far rather
// than replacing it, so a sparse file changes only the keys it mentions.
//
// Files are discovered under the XDG base directories and their Windows
// equivalents, and in the working directory, using the application name set by
// [WithAppName] and the extensions the codec registry serves. [WithExplicitPath]
// loads one named file instead of discovering any.
//
// # Provenance
//
// [Load] returns a [Metadata] alongside the value. It records, for every key
// that resolved, the layer that supplied it, the file or variable that carried
// it, and the raw text of the value. [Metadata.Where] looks one key up, and
// [Metadata.ActiveFiles] lists the files that contributed.
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
