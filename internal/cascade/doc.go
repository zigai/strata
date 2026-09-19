// Package cascade discovers the configuration files that make up the layered
// tiers, and orders them by precedence.
//
// A layer carries either the path of a file to read or, for standard input, the
// data itself. Reading and decoding belong to the caller, so this package
// decides only what to read and in which order.
package cascade
