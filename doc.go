// Package strata loads layered configuration and records each value's source.
//
// # Layers
//
// Configuration is merged in ascending precedence:
//
//	struct defaults -> system file -> user file -> config file -> environment -> CLI flags
//
// Each source overlays only the keys it sets. All layers beyond defaults are
// optional: [WithAppName] finds system and user files, [WithPath] and
// [WithOptionalPath] select a file, [WithEnvPrefix] binds environment variables,
// and the CLI bridges apply flags.
//
// File loading requires [WithFormats]. Only listed formats are discovered or
// decoded. The first listed format is used for stdin and new user files.
//
// # Provenance
//
// [LoadWithMetadata] returns the value and its [Metadata], including each key's
// source and raw text. [Metadata.Where] looks up one key,
// [Metadata.Origins] lists all origins, and [Metadata.ActiveFiles] lists files
// that contributed.
//
// [Metadata.UnknownKeys] lists undeclared file keys and suggested matches;
// [WithStrict] makes them errors.
//
// # Extension points
//
// [Defaulter] supplies defaults; [Validator] and [MetadataValidator] validate
// the merged result. [WithCodec], [WithDecoder], and [WithDecoderFunc] register
// readers for one load. [Save], [Init], and [Set] write only built-in TOML,
// YAML, and JSON formats.
//
// # Errors
//
// Named failures have package sentinels for [errors.Is], including aliases of
// errors raised by internal packages. Other errors retain their original type,
// such as [*fs.PathError] for file access failures.
//
// # Layout
//
// The codec package defines formats. CLI bridges live in separate modules.
package strata
