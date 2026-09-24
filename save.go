package strata

import (
	"fmt"
	"os"
	"path/filepath"

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

// Save writes all of cfg to targetPath, replacing the file atomically. Existing
// content is discarded; use [Set] to preserve it.
//
// The extension selects a built-in format. Unsupported extensions return
// [ErrUnsupportedFormat]. Files use mode 0o644 unless [WithFileMode] changes it.
func Save[T any](targetPath string, cfg T, opts ...SaveOption) error {
	options := &saveOptions{
		mode: 0o644,
	}
	for _, opt := range opts {
		opt(options)
	}

	ext := normalizeExt(filepath.Ext(targetPath))

	c, ok := builtinFormats.Get(ext)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, ext)
	}

	encoded, err := c.Encode(cfg)
	if err != nil {
		return fmt.Errorf("encode configuration for %s: %w", targetPath, err)
	}

	if err := atomicfile.WriteFileAtomic(targetPath, encoded, options.mode); err != nil {
		return fmt.Errorf("save configuration to %s: %w", targetPath, err)
	}

	return nil
}
