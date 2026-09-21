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

// WriteFileAtomic writes data to targetPath atomically, using a temporary file
// in the destination directory as the intermediate.
//
// Missing parent directories are created. The data is written, synced, and
// renamed into place, in that order.
//
// The resulting file carries exactly the permission bits in perm. A perm of zero
// selects 0o644.
//
// Because the file becomes visible under targetPath only at the rename, an
// interrupted write leaves either the previous file or the complete new one,
// never a partial file.
//
// NB: an error that wraps the directory sync means the new content is in place
// but may not survive a crash. The rename is already committed at that point.
func WriteFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(targetPath, data, perm, true)
}

// CreateFileAtomic writes data to targetPath atomically without overwriting an existing file.
// If targetPath already exists, it returns an error matching os.ErrExist / fs.ErrExist.
func CreateFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(targetPath, data, perm, false)
}

func writeFileAtomic(targetPath string, data []byte, perm os.FileMode, overwrite bool) (err error) {
	if perm == 0 {
		perm = defaultFilePerm
	}

	dir := filepath.Dir(targetPath)
	if mkErr := os.MkdirAll(dir, defaultDirPerm); mkErr != nil {
		return fmt.Errorf("create directory %s: %w", dir, mkErr)
	}

	tmpFile, cErr := os.CreateTemp(dir, fmt.Sprintf(".%s.tmp.*", filepath.Base(targetPath)))
	if cErr != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, cErr)
	}

	tmpPath := tmpFile.Name()

	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()

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

	if overwrite {
		if rErr := replaceFile(tmpPath, targetPath); rErr != nil {
			return fmt.Errorf("replace file %s with %s: %w", targetPath, tmpPath, rErr)
		}
	} else {
		if cErr := createFileNoOverwrite(tmpPath, targetPath); cErr != nil {
			return cErr
		}
	}

	if sErr := syncDir(dir); sErr != nil {
		return fmt.Errorf("sync directory %s: %w", dir, sErr)
	}

	return nil
}
