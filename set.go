package strata

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/strata/internal/atomicfile"
	"github.com/zigai/strata/internal/edit"
	"github.com/zigai/strata/internal/stream"
)

// SetBytes updates one key in raw configuration bytes and returns the result.
//
// The document is not reduced. Every comment, key, and value outside the one
// replaced survives, including YAML anchors and aliases and TOML multi-line
// values. What the editors rewrite is presentation, and how much differs:
//
//   - TOML is edited line by line, so comments, blank lines, indentation, and
//     key order are kept. The padding between a value and its trailing comment
//     collapses to one space, and an appended key is preceded by a blank line.
//   - YAML keeps comments, key order, anchors, aliases, block scalars, merge
//     keys, flow style, quoting, and the document's indentation. It drops every
//     blank line, because a parsed document does not represent them.
//   - JSON keeps the document's indentation, sorts object keys, and adds a
//     trailing newline. Values survive, and an untouched number keeps its exact
//     digits.
//
// Two documents are rejected rather than rewritten, because re-encoding would
// discard content: a YAML stream carrying more than one document, and a JSON
// object with a duplicate member.
//
// The format argument accepts an extension with or without its leading dot and
// is matched case-insensitively: ".toml", "toml", and ".TOML" are equivalent.
// An extension that matches no supported format returns an error wrapping
// [ErrUnsupportedFormat].
//
// A string value is type-inferred from its text before it is written. The
// inferred type is the one a configuration file expects for text supplied on a
// command line: "9090" becomes the integer 9090, "true" becomes a boolean, and
// "1e5" becomes the number 100000. A single letter such as "t" stays a string;
// boolean inference recognizes only the full words "true" and "false".
func SetBytes(format string, data []byte, dottedKey string, value any) ([]byte, error) {
	val := inferCLIValue(value)
	ext := normalizeExt(format)

	switch ext {
	case ".yaml", ".yml":
		updated, err := edit.UpdateYAML(data, dottedKey, val)
		if err != nil {
			return nil, fmt.Errorf("update yaml: %w", err)
		}

		return updated, nil
	case ".toml":
		updated, err := edit.UpdateTOML(data, dottedKey, val)
		if err != nil {
			return nil, fmt.Errorf("update toml: %w", err)
		}

		return updated, nil
	case ".json":
		updated, err := edit.UpdateJSON(data, dottedKey, val)
		if err != nil {
			return nil, fmt.Errorf("update json: %w", err)
		}

		return updated, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, ext)
	}
}

// Set updates one key in the file at targetPath.
//
// Nothing outside the replaced key is dropped, and the document keeps the
// indentation it was written with. [SetBytes] states what else each editor
// rewrites; the entry worth knowing before editing a file a human maintains is
// that a YAML document loses its blank lines.
//
// Values are interpreted as in [SetBytes].
//
// The rewritten file replaces the original atomically. An interruption leaves
// either the previous file or the new one, never a partial write.
//
// The replacement keeps the permission bits of the file being edited: a 0600 or
// 0640 configuration stays that way.
func Set(targetPath string, dottedKey string, value any) error {
	resolvedPath, symErr := filepath.EvalSymlinks(targetPath)
	if symErr == nil {
		targetPath = resolvedPath
	}

	f, oErr := os.Open(targetPath)
	if oErr != nil {
		return fmt.Errorf("open target configuration %s: %w", targetPath, oErr)
	}

	info, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return fmt.Errorf("stat target configuration %s: %w", targetPath, statErr)
	}

	data, rErr := stream.ReadBounded(f, stream.DefaultMaxFileSize)
	if cErr := f.Close(); cErr != nil && rErr == nil {
		return fmt.Errorf("close target configuration %s: %w", targetPath, cErr)
	}

	if rErr != nil {
		return fmt.Errorf("read target configuration %s: %w", targetPath, rErr)
	}

	ext := filepath.Ext(targetPath)

	updated, sErr := SetBytes(ext, data, dottedKey, value)
	if sErr != nil {
		return fmt.Errorf("update %s in %s: %w", dottedKey, targetPath, sErr)
	}

	if wErr := atomicfile.WriteFileAtomic(targetPath, updated, info.Mode().Perm()); wErr != nil {
		return fmt.Errorf("write updated configuration to %s: %w", targetPath, wErr)
	}

	return nil
}

func inferCLIValue(val any) any {
	s, ok := val.(string)
	if !ok {
		return val
	}

	return inferCLIString(s)
}

func inferCLIString(s string) any {
	trimmed := strings.TrimSpace(s)
	if i, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return i
	}

	if u, err := strconv.ParseUint(trimmed, 10, 64); err == nil {
		return u
	}

	if strings.EqualFold(trimmed, "true") {
		return true
	}

	if strings.EqualFold(trimmed, "false") {
		return false
	}

	if strings.Contains(trimmed, ".") || strings.ContainsAny(trimmed, "eE") {
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return f
		}
	}

	return s
}
