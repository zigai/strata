package origins

import "strings"

// Record is one configuration key found in a document.
type Record struct {
	// Key is the dotted key, with each segment converted to snake_case so that
	// it matches the key every other tier records.
	Key string

	// Line is the line that defined the key.
	//
	// NB: Only the YAML reader populates this. It walks a node tree that carries
	// positions; the TOML and JSON readers report zero.
	Line int

	// RawValue is the value as text, with a string's surrounding quotes removed.
	RawValue string
}

// Read reports one Record per key in data, using the reader for format.
//
// format accepts an extension with or without its leading dot and is matched
// case-insensitively. When format names no reader, TOML, YAML, and JSON are
// tried in turn and the first that parses wins.
//
// A document that no reader can parse reports nothing. Read never fails: a
// caller that needs the document decoded has already done so through a codec,
// and provenance is a description of a document that is known to be valid.
func Read(data []byte, format string, emit func(Record)) {
	ext := strings.ToLower(strings.TrimSpace(format))
	if !strings.HasPrefix(ext, ".") && ext != "" {
		ext = "." + ext
	}

	switch ext {
	case ".yaml", ".yml":
		readYAML(data, emit)
	case ".toml":
		readTOML(data, emit)
	case ".json":
		readJSON(data, emit)
	default:
		if !readTOML(data, emit) && !readYAML(data, emit) {
			readJSON(data, emit)
		}
	}
}
