package origins

import "strings"

type Record struct {
	Key string

	// Only the YAML reader populates Line; TOML and JSON report zero.
	Line int

	RawValue string

	// IsTemplate reports that the key sits under a YAML anchor the document
	// reuses through an alias, as in a `base: &b {...}` entry that exists to be
	// merged elsewhere. Only the YAML reader sets it.
	IsTemplate bool
}

func Read(data []byte, ext string, emit func(Record)) {
	// Provenance describes a document the codec already accepted, so an unreadable
	// document reports nothing rather than failing the load.
	ext = strings.ToLower(strings.TrimSpace(ext))
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
