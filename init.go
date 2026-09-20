package strata

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zigai/strata/internal/atomicfile"
	"github.com/zigai/strata/internal/defaulter"
)

var (
	// ErrFileExists is returned when [Init] targets an existing file and
	// overwriting was not requested.
	ErrFileExists = errors.New("configuration file already exists")

	// ErrUnsupportedFormat is returned when a file extension matches no supported
	// configuration format.
	ErrUnsupportedFormat = errors.New("unsupported configuration format")
)

type initOptions struct {
	schemaURL string
	overwrite bool
}

// InitOption configures [Init].
type InitOption func(*initOptions)

// WithSchemaURL sets the schema URL that [Init] writes into the generated
// template for editors and language servers.
func WithSchemaURL(url string) InitOption {
	return func(o *initOptions) {
		o.schemaURL = url
	}
}

// WithOverwrite sets whether [Init] may replace an existing configuration file.
// The default is false, which reports an existing file as [ErrFileExists].
func WithOverwrite(overwrite bool) InitOption {
	return func(o *initOptions) {
		o.overwrite = overwrite
	}
}

// Init writes a configuration template for T to targetPath.
//
// The template holds the defaults of T, encoded in the format implied by the
// file extension. When [WithSchemaURL] is set, the schema URL is recorded for
// editors: as the "#:schema <url>" comment header for TOML, the
// "# yaml-language-server: $schema=<url>" header for YAML, and a top-level
// "$schema" property for JSON.
//
// An existing file is left untouched and reported as [ErrFileExists], unless
// [WithOverwrite] is set. An extension that matches no supported format is
// reported as [ErrUnsupportedFormat]. A codec bound for one load with [WithCodec]
// takes part in loading, not in writing.
//
// The write is atomic. The file is staged beside the target and renamed into
// place. A failure does not leave a partial template.
func Init[T any](targetPath string, opts ...InitOption) error {
	options := &initOptions{
		schemaURL: "",
		overwrite: false,
	}
	for _, opt := range opts {
		opt(options)
	}

	if !options.overwrite {
		// NB: any stat failure other than "not exist" means the path is there but
		// unreachable. It must not be taken for a free path.
		_, statErr := os.Stat(targetPath)

		switch {
		case statErr == nil:
			return fmt.Errorf("%w: %s", ErrFileExists, targetPath)
		case !errors.Is(statErr, fs.ErrNotExist):
			return fmt.Errorf("stat %s: %w", targetPath, statErr)
		}
	}

	var target T
	if err := defaulter.Apply(&target, nil); err != nil {
		return fmt.Errorf("apply defaults for template: %w", err)
	}

	data, err := formatInitData(target, filepath.Ext(targetPath), options.schemaURL)
	if err != nil {
		return err
	}

	if err := atomicfile.WriteFileAtomic(targetPath, data, 0o644); err != nil {
		return fmt.Errorf("write template file %s: %w", targetPath, err)
	}

	return nil
}

func formatInitData(target any, ext string, schemaURL string) ([]byte, error) {
	normalized := normalizeExt(ext)

	c, ok := builtinFormats.Get(normalized)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, normalized)
	}

	encoded, err := c.Encode(target)
	if err != nil {
		return nil, fmt.Errorf("encode %s template: %w", formatName(normalized), err)
	}

	switch normalized {
	case ".toml":
		if schemaURL != "" {
			header := fmt.Sprintf("#:schema %s\n\n", schemaURL)
			return append([]byte(header), encoded...), nil
		}
	case ".yaml", ".yml":
		if schemaURL != "" {
			header := fmt.Sprintf("# yaml-language-server: $schema=%s\n\n", schemaURL)
			return append([]byte(header), encoded...), nil
		}
	case ".json":
		if schemaURL != "" {
			return injectJSONSchema(encoded, schemaURL)
		}
	}

	return encoded, nil
}

func injectJSONSchema(data []byte, schemaURL string) ([]byte, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return data, nil
	}

	// NB: writing the entry first keeps "$schema" the object's first key, and the
	// existing content follows it.
	schemaEntry, err := json.Marshal(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("marshal schema url: %w", err)
	}

	var b bytes.Buffer
	b.WriteString("{\n  \"$schema\": ")
	b.Write(schemaEntry)

	// NB: an object with no content beyond its braces takes no trailing comma.
	rest := bytes.TrimSpace(trimmed[1:])
	if len(rest) > 1 && rest[0] != '}' {
		b.WriteString(",\n  ")
		b.Write(bytes.TrimLeft(rest, "\n "))
	} else {
		b.WriteString("\n}")
	}

	return b.Bytes(), nil
}
