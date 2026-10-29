//go:build unix && !aix && !zos

package atomicfile

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(file *os.File) error {
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX)
		if err == nil {
			return nil
		}

		if !errors.Is(err, unix.EINTR) {
			return fmt.Errorf("flock: %w", err)
		}
	}
}

func unlockFile(file *os.File) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("flock: %w", err)
	}

	return nil
}
