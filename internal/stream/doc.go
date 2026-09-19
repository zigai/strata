// Package stream reads configuration input under a size bound.
//
// Input larger than the bound is reported rather than truncated, so a truncated
// prefix is never mistaken for a complete document. The process standard input
// is read once and cached for the lifetime of the process, because a stream
// cannot be re-read.
package stream
