//go:build !windows && !(unix && !aix && !zos)

package atomicfile

import (
	"errors"
	"os"
)

func lockFile(*os.File) error {
	return errors.ErrUnsupported
}

func unlockFile(*os.File) error {
	return errors.ErrUnsupported
}
