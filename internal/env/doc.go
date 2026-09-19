// Package env binds environment variables onto the fields of a struct.
//
// A field opts in through an "env" tag or through the configured prefix and the
// key derived for it. A value that does not decode into its field's type is
// reported rather than wrapped or saturated, and a field marked secret never has
// its value reproduced in an error or in the reported provenance.
package env
