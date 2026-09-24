// Package codec defines configuration formats and their file extension registry.
//
// Built-in codecs support TOML, YAML, and JSON. Callers can register codecs for
// one load with strata.WithCodec. See [Codec] for the decoding contract.
package codec
