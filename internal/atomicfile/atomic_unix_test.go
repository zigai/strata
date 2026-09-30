//go:build unix

package atomicfile_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zigai/strata/internal/atomicfile"
)

func dirNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

// When the write cannot be staged beside the target, it fails and the
// original is untouched. A writer that fell back to rewriting the file in
// place would succeed here, since the file itself stays writable.
func TestFailedStagingLeavesTheOriginal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(target, []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := atomicfile.Write(target, []byte("version = 2\n"), 0o644); err == nil {
		t.Fatal("WriteFileAtomic succeeded in a read-only directory")
	}

	if data, err := os.ReadFile(target); err != nil || string(data) != "version = 1\n" {
		t.Fatalf("original = %q, %v; want it unchanged", data, err)
	}

	if names := dirNames(t, dir); !slices.Equal(names, []string{"config.toml"}) {
		t.Fatalf("directory holds %v, want only config.toml", names)
	}
}

func TestFailedCommitRemovesTheStagingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "config.toml")

	// A non-empty directory at the target path makes the final rename fail.
	if err := os.MkdirAll(filepath.Join(target, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := atomicfile.Write(target, []byte("version = 2\n"), 0o644); err == nil {
		t.Fatal("WriteFileAtomic replaced a non-empty directory")
	}

	if names := dirNames(t, dir); !slices.Equal(names, []string{"config.toml"}) {
		t.Fatalf("directory holds %v, want only config.toml", names)
	}

	if names := dirNames(t, target); !slices.Equal(names, []string{"keep"}) {
		t.Fatalf("target directory holds %v, want it untouched", names)
	}
}

func TestReaderOpenedBeforeTheWriteSeesTheOldFile(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(target, []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reader, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = reader.Close() })

	if err := atomicfile.Write(target, []byte("version = 22\n"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	old := make([]byte, 64)

	n, err := reader.Read(old)
	if err != nil || string(old[:n]) != "version = 1\n" {
		t.Fatalf("old handle reads %q, %v; want the old file", old[:n], err)
	}

	if data, err := os.ReadFile(target); err != nil || string(data) != "version = 22\n" {
		t.Fatalf("new open reads %q, %v; want the new file", data, err)
	}
}
