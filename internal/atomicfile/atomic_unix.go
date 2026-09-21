//go:build !windows

package atomicfile

import (
	"fmt"
	"os"
)

func replaceFile(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("rename %s to %s: %w", from, to, err)
	}

	return nil
}

func createFileNoOverwrite(from, to string) error {
	if err := os.Link(from, to); err != nil {
		return fmt.Errorf("link %s to %s: %w", from, to, err)
	}

	_ = os.Remove(from)

	return nil
}

func syncDir(dir string) (err error) {
	d, oErr := os.Open(dir)
	if oErr != nil {
		return fmt.Errorf("open directory %s: %w", dir, oErr)
	}

	defer func() {
		if cErr := d.Close(); cErr != nil && err == nil {
			err = fmt.Errorf("close directory %s: %w", dir, cErr)
		}
	}()

	if sErr := d.Sync(); sErr != nil {
		return fmt.Errorf("sync directory %s: %w", dir, sErr)
	}

	return nil
}
