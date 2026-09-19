// Package edit replaces one key in a configuration document while keeping as
// much of the document's layout as the format allows.
//
// Three editors are provided, one per format, and they preserve different
// amounts:
//
//   - TOML is edited line by line, so indentation and the surrounding text are
//     kept exactly.
//   - YAML keeps comments, keys, and the document's indentation. Blank lines do
//     not survive, because a parsed document does not represent them.
//   - JSON keeps the document's indentation. Object keys are sorted, and a
//     trailing newline is added. JSON carries no comments.
//
// An editor refuses input it cannot rewrite without losing content, rather than
// silently discarding the parts it does not understand.
package edit
