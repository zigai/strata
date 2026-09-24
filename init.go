package strata

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zigai/strata/codec"
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

// Init writes T's defaults as a configuration template at targetPath. The file
// extension selects a built-in format. Secret fields are omitted.
// [WithSchemaURL] adds a #:schema header in TOML, a yaml-language-server
// header in YAML, or a $schema property in JSON.
//
// Existing files remain untouched and return [ErrFileExists] unless
// [WithOverwrite] is set. Unsupported extensions return
// [ErrUnsupportedFormat]. The write is staged beside the target and renamed
// atomically; a failure does not leave a partial template. Per-load codecs
// registered with [WithCodec] do not take part in writing.
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

	data, err := formatInitData(codec.WithoutSecrets(target), filepath.Ext(targetPath), options.schemaURL)
	if err != nil {
		return err
	}

	return writeInitFile(targetPath, data, options.overwrite)
}

func writeInitFile(targetPath string, data []byte, overwrite bool) error {
	if overwrite {
		if err := atomicfile.WriteFileAtomic(targetPath, data, 0o644); err != nil {
			return fmt.Errorf("write template file %s: %w", targetPath, err)
		}

		return nil
	}

	if err := atomicfile.CreateFileAtomic(targetPath, data, 0o644); err != nil {
		if errors.Is(err, fs.ErrExist) || os.IsExist(err) {
			return fmt.Errorf("%w: %s", ErrFileExists, targetPath)
		}

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
