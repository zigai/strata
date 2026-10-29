package strata

import (
	"errors"
	"fmt"
	"io/fs"
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
// The default is 0o644. An existing file keeps its own permission bits.
func WithFileMode(mode os.FileMode) SaveOption {
	return func(opts *saveOptions) {
		opts.mode = mode
	}
}

// Save writes all of cfg to targetPath, replacing the file atomically. Existing
// content is discarded; use [Set] to preserve it.
//
// The extension selects a built-in format. Unsupported extensions return
// [ErrUnsupportedFormat]. A symlink is followed and the file it points to is
// written. An existing file keeps its permission bits; a new file uses 0o644
// unless [WithFileMode] changes it. Save takes the same writer lock as [SetAll].
func Save[T any](targetPath string, cfg T, opts ...SaveOption) (err error) {
	options := &saveOptions{
		mode: 0o644,
	}
	for _, opt := range opts {
		opt(options)
	}

	resolvedPath, err := atomicfile.Resolve(targetPath)
	if err != nil {
		return fmt.Errorf("save configuration: %w", err)
	}

	ext := normalizeExt(filepath.Ext(resolvedPath))

	c, ok := builtinFormats.Get(ext)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, ext)
	}

	encoded, err := c.Encode(cfg)
	if err != nil {
		return fmt.Errorf("encode configuration for %s: %w", targetPath, err)
	}

	unlock, err := lockForWrite(resolvedPath)
	if err != nil {
		return fmt.Errorf("save configuration to %s: %w", targetPath, err)
	}

	defer func() { err = errors.Join(err, unlock()) }()

	mode, err := existingMode(resolvedPath, options.mode)
	if err != nil {
		return fmt.Errorf("save configuration to %s: %w", targetPath, err)
	}

	if err := atomicfile.Write(resolvedPath, encoded, mode); err != nil {
		return fmt.Errorf("save configuration to %s: %w", targetPath, err)
	}

	return nil
}

func lockForWrite(targetPath string) (func() error, error) {
	lock, err := atomicfile.Acquire(targetPath)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", targetPath, err)
	}

	return func() error {
		if err := lock.Release(); err != nil {
			return fmt.Errorf("unlock %s: %w", targetPath, err)
		}

		return nil
	}, nil
}

func existingMode(targetPath string, fallback os.FileMode) (os.FileMode, error) {
	info, err := os.Stat(targetPath)
	if errors.Is(err, fs.ErrNotExist) {
		return fallback, nil
	}

	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", targetPath, err)
	}

	return info.Mode().Perm(), nil
}
