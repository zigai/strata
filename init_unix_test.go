//go:build unix

package strata_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/zigai/strata"
)

// A path Init cannot stat, for a reason other than not existing, is reported
// rather than taken as free. A symlink that points at itself fails stat with
// ELOOP, and must be neither replaced nor reported as an existing file.
func TestInitReportsStatErrors(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(path, path); err != nil {
		t.Fatal(err)
	}

	err := strata.Init[demoConfig](path)
	if !errors.Is(err, syscall.ELOOP) || errors.Is(err, strata.ErrFileExists) {
		t.Fatalf("err = %v, want the stat error ELOOP", err)
	}

	if target, err := os.Readlink(path); err != nil || target != path {
		t.Fatalf("symlink = %q, %v; want it untouched", target, err)
	}
}
