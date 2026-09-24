package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultDirPerm  os.FileMode = 0o755
	defaultFilePerm os.FileMode = 0o644
)

// WriteFileAtomic writes data to targetPath through a temporary file in the
// same directory.
//
// Parent directories are created if needed. Data is written, synced, and
// renamed; an interrupted write leaves the old or complete new file. A zero
// perm uses 0o644.
//
// A directory sync error can occur after the rename, when the new file is in
// place but may not survive a crash.
func WriteFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(targetPath, data, perm, true)
}

// CreateFileAtomic writes data to targetPath atomically without overwriting an existing file.
// If targetPath already exists, it returns an error matching [os.ErrExist].
func CreateFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(targetPath, data, perm, false)
}

func writeTempFile(tmpFile *os.File, data []byte, perm os.FileMode) error {
	tmpPath := tmpFile.Name()

	// NB: os.CreateTemp always creates 0o600; perm must be applied explicitly.
	if chErr := tmpFile.Chmod(perm); chErr != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("set permissions %#o on temporary file %s: %w", perm, tmpPath, chErr)
	}

	if _, wErr := tmpFile.Write(data); wErr != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temporary file %s: %w", tmpPath, wErr)
	}

	if sErr := tmpFile.Sync(); sErr != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temporary file %s: %w", tmpPath, sErr)
	}

	if cErr := tmpFile.Close(); cErr != nil {
		return fmt.Errorf("close temporary file %s: %w", tmpPath, cErr)
	}

	return nil
}

func commitAtomicFile(tmpPath, targetPath string, overwrite bool) error {
	if overwrite {
		if rErr := replaceFile(tmpPath, targetPath); rErr != nil {
			return fmt.Errorf("replace file %s with %s: %w", targetPath, tmpPath, rErr)
		}

		return nil
	}

	return createFileNoOverwrite(tmpPath, targetPath)
}

func writeFileAtomic(targetPath string, data []byte, perm os.FileMode, overwrite bool) error {
	if perm == 0 {
		perm = defaultFilePerm
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, defaultDirPerm); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, fmt.Sprintf(".%s.tmp.*", filepath.Base(targetPath)))
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}

	tmpPath := tmpFile.Name()

	var success bool
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := writeTempFile(tmpFile, data, perm); err != nil {
		return err
	}

	if err := commitAtomicFile(tmpPath, targetPath, overwrite); err != nil {
		return err
	}

	success = true

	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync directory %s: %w", dir, err)
	}

	return nil
}
