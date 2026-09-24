// Package edit replaces one configuration key while preserving available layout.
//
// TOML keeps surrounding text; YAML keeps comments and indentation but drops
// blank lines; JSON keeps indentation, sorts keys, and adds a trailing newline.
//
// Editors reject input they cannot rewrite without losing content.
package edit
