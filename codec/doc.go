// Package codec defines the decoding and encoding contract for one
// configuration format, and the registry that maps file extensions onto it.
//
// A Codec is stateless and safe for concurrent use, so a single value may be
// held in a [Registry] and called from several goroutines. The built-in codecs
// cover TOML, YAML, and JSON; a caller adds its own for one load through
// strata.WithCodec.
//
// # Errors
//
// [ErrMalformed] is returned by any Decode given a document it cannot parse. The
// parser's own error stays reachable with [errors.As], but its type varies by
// format and by failure, so classify with [ErrMalformed].
//
// [ErrNilTarget] is returned by any Decode given a nil target.
// [ErrMultipleDocuments] is returned by [YAMLCodec.Decode] for a stream carrying
// more than one document, which a single configuration tier cannot represent.
package codec
