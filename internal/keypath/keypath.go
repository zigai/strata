// Package keypath converts between the dotted configuration keys of the
// public API and the JSON pointers strata uses to identify keys inside.
//
// A dotted key separates mapping keys with dots. Inside one key, \. is a
// literal dot and \\ a literal backslash; no other escape exists. Every other
// character, including / and ~, stands for itself, and an empty key between
// two dots, or before or after one, is the empty mapping key.
package keypath

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

var (
	// ErrEmpty is returned for the empty string, which names no key at all.
	ErrEmpty = errors.New("invalid empty key path")

	// ErrInvalid is returned for a key with a backslash that starts neither
	// escape, or that is not valid UTF-8.
	ErrInvalid = errors.New("invalid configuration key")
)

// Parse splits a dotted key into the tokens of a JSON pointer.
func Parse(key string) (jsontext.Pointer, error) {
	if key == "" {
		return "", ErrEmpty
	}

	if !utf8.ValidString(key) {
		return "", fmt.Errorf("%w %q: not valid UTF-8", ErrInvalid, key)
	}

	var (
		pointer jsontext.Pointer
		token   strings.Builder
	)

	for index := 0; index < len(key); index++ {
		switch char := key[index]; char {
		case '.':
			pointer = pointer.AppendToken(token.String())
			token.Reset()
		case '\\':
			index++
			if index == len(key) {
				return "", fmt.Errorf(`%w "%s": it ends in a backslash; write \\ for a literal one`, ErrInvalid, key)
			}

			if next := key[index]; next != '.' && next != '\\' {
				escaped, _ := utf8.DecodeRuneInString(key[index:])
				return "", fmt.Errorf(`%w "%s": \%c is not an escape; write \. for a literal dot and \\ for a backslash`, ErrInvalid, key, escaped)
			}

			token.WriteByte(key[index])
		default:
			token.WriteByte(char)
		}
	}

	return pointer.AppendToken(token.String()), nil
}

// Format writes key in dotted form, escaping each dot and backslash inside a
// token. The root and a single empty token both format as the empty string.
func Format(key jsontext.Pointer) string {
	var b strings.Builder

	first := true
	for token := range key.Tokens() {
		if !first {
			b.WriteByte('.')
		}

		first = false

		for index := range len(token) {
			if token[index] == '.' || token[index] == '\\' {
				b.WriteByte('\\')
			}

			b.WriteByte(token[index])
		}
	}

	return b.String()
}
