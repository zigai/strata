//go:build windows

package atomicfile

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func replaceFile(from, to string) error {
	from16, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return fmt.Errorf("convert from path %s to utf16: %w", from, err)
	}

	to16, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return fmt.Errorf("convert to path %s to utf16: %w", to, err)
	}

	flags := uint32(windows.MOVEFILE_REPLACE_EXISTING | windows.MOVEFILE_WRITE_THROUGH)
	if err := windows.MoveFileEx(from16, to16, flags); err != nil {
		return fmt.Errorf("MoveFileEx %s to %s: %w", from, to, err)
	}

	return nil
}

func syncDir(_ string) error {
	return nil
}
