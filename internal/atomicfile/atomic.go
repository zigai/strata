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

func Write(targetPath string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(targetPath, data, perm, true)
}

func Create(targetPath string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(targetPath, data, perm, false)
}

func writeTempFile(tmpFile *os.File, data []byte, perm os.FileMode) error {
	tmpPath := tmpFile.Name()

	// NB: os.CreateTemp always creates 0o600; perm must be applied explicitly.
	if chmodErr := tmpFile.Chmod(perm); chmodErr != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("set permissions %#o on temporary file %s: %w", perm, tmpPath, chmodErr)
	}

	if _, writeErr := tmpFile.Write(data); writeErr != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temporary file %s: %w", tmpPath, writeErr)
	}

	if syncErr := tmpFile.Sync(); syncErr != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temporary file %s: %w", tmpPath, syncErr)
	}

	if closeErr := tmpFile.Close(); closeErr != nil {
		return fmt.Errorf("close temporary file %s: %w", tmpPath, closeErr)
	}

	return nil
}

func commitAtomicFile(tmpPath, targetPath string, overwrite bool) error {
	if overwrite {
		if replaceErr := replaceFile(tmpPath, targetPath); replaceErr != nil {
			return fmt.Errorf("replace file %s with %s: %w", targetPath, tmpPath, replaceErr)
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

	// A directory sync error can occur after the rename, when the new file is in
	// place but may not survive a crash.
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync directory %s: %w", dir, err)
	}

	return nil
}
