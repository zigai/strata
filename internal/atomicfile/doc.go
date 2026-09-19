// Package atomicfile replaces a file's contents in a way that survives
// interruption.
//
// The new contents are written to a temporary file in the destination directory,
// synced, and renamed over the destination, so a concurrent reader sees either
// the previous file or the complete new one, never a partial file.
package atomicfile
