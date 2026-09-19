package strata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/strata/codec"
	"github.com/zigai/strata/internal/atomicfile"
)

type saveOptions struct {
	mode os.FileMode
}

// SaveOption configures [Save].
type SaveOption func(*saveOptions)

// WithFileMode sets the permission bits used when [Save] creates the file.
// The default is 0o644.
func WithFileMode(mode os.FileMode) SaveOption {
	return func(o *saveOptions) {
		o.mode = mode
	}
}

// Save writes the complete T to targetPath, replacing the file atomically.
//
// The output is the encoded value of cfg. No schema directive is added, and
// nothing the previous file contained is preserved, unlike [Init]. Save is
// intended for files the application owns outright; [Set] is intended for files
// a human maintains.
//
// The file extension selects the codec. An extension that matches no supported
// format is reported as [ErrUnsupportedFormat]. The file is created with mode
// 0o644 unless [WithFileMode] sets another one.
func Save[T any](targetPath string, cfg T, opts ...SaveOption) error {
	options := &saveOptions{
		mode: 0o644,
	}
	for _, opt := range opts {
		opt(options)
	}

	ext := strings.ToLower(filepath.Ext(targetPath))

	var (
		encoded []byte
		err     error
	)

	switch ext {
	case ".toml":
		encoded, err = codec.NewTOMLCodec().Encode(cfg)
	case ".yaml", ".yml":
		encoded, err = codec.NewYAMLCodec().Encode(cfg)
	case ".json":
		encoded, err = codec.NewJSONCodec().Encode(cfg)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, ext)
	}

	if err != nil {
		return fmt.Errorf("encode configuration for %s: %w", targetPath, err)
	}

	if err := atomicfile.WriteFileAtomic(targetPath, encoded, options.mode); err != nil {
		return fmt.Errorf("save configuration to %s: %w", targetPath, err)
	}

	return nil
}
