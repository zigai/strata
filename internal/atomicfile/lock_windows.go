//go:build windows

package atomicfile

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockFile(file *os.File) error {
	var overlapped windows.Overlapped

	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("LockFileEx: %w", err)
	}

	return nil
}

func unlockFile(file *os.File) error {
	var overlapped windows.Overlapped

	if err := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("UnlockFileEx: %w", err)
	}

	return nil
}
