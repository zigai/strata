// Package origins extracts the keys of a configuration document, with the raw
// text and line of each value, for provenance reporting.
//
// The extraction is independent of decoding: a document is read a second time
// here, in a shape that preserves the raw text of each value, which the typed
// decode discards.
package origins
