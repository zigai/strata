// Package codec defines configuration formats and their file extension registry.
//
// Built-in codecs support TOML, YAML, and JSON. Callers can register codecs for
// one load with strata.WithCodec. See [Codec] for the decoding contract.
//
// The built-in codecs decode a document the same way. Tables and maps merge
// into the values already in the target, so a later layer that names one map
// entry keeps the entry's other fields. A sequence replaces the current slice,
// and a fixed-size array needs exactly its number of elements. A null, which
// TOML cannot express, resets the field to its zero value. A [time.Duration]
// is read only from a Go duration string such as "1m30s". Every value that
// cannot be decoded is reported as a [KeyError] naming its configuration key.
package codec
