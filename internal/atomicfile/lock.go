package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Lock is an exclusive advisory lock held by one writer of a target file.
type Lock struct {
	file *os.File
}

// Acquire blocks until the caller holds the writer lock for targetPath. The
// lock lives on a ".<name>.lock" file beside the target rather than on the
// target itself, because an atomic write replaces the target's inode. The lock
// file is left in place: removing it would let a waiting writer lock an
// unlinked file while a new writer locks its replacement. A missing parent
// directory is created.
func Acquire(targetPath string) (*Lock, error) {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, defaultDirPerm); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", dir, err)
	}

	lockPath := filepath.Join(dir, "."+filepath.Base(targetPath)+".lock")

	file, err := os.OpenFile(lockPath, os.O_RDONLY|os.O_CREATE, defaultFilePerm)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}

	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock %s: %w", lockPath, err)
	}

	return &Lock{file: file}, nil
}

// Release gives up the lock.
func (l *Lock) Release() error {
	unlockErr := unlockFile(l.file)
	closeErr := l.file.Close()

	if unlockErr != nil {
		return fmt.Errorf("unlock %s: %w", l.file.Name(), unlockErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close lock file %s: %w", l.file.Name(), closeErr)
	}

	return nil
}

// Resolve returns the file a write to targetPath replaces: the end of its
// symlink chain, even when that end does not exist yet, so a write updates the
// linked file instead of replacing the link.
func Resolve(targetPath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(targetPath)
	if err == nil {
		return resolved, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		// EvalSymlinks reports a loop with its own error text; stat names the
		// operating system's error, such as ELOOP.
		if _, statErr := os.Stat(targetPath); statErr != nil {
			err = statErr
		}

		return "", fmt.Errorf("resolve %s: %w", targetPath, err)
	}

	info, statErr := os.Lstat(targetPath)
	if errors.Is(statErr, fs.ErrNotExist) || statErr == nil && info.Mode()&fs.ModeSymlink == 0 {
		return targetPath, nil
	}

	if statErr != nil {
		return "", fmt.Errorf("stat %s: %w", targetPath, statErr)
	}

	link, err := os.Readlink(targetPath)
	if err != nil {
		return "", fmt.Errorf("read symlink %s: %w", targetPath, err)
	}

	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(targetPath), link)
	}

	return Resolve(link)
}
