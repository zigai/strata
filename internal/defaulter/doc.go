// Package defaulter walks a struct value and applies the defaults it declares.
//
// The walk recurses into nested structs and embedded pointers, and it derives
// the configuration key of each field. That derivation is shared by every tier
// and by both CLI bridges, so a key names the same field everywhere.
package defaulter
