package atomicfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/strata/internal/atomicfile"
)

func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "nested", "dir", "config.toml")
	data1 := []byte("version = 1\n")

	if err := atomicfile.WriteFileAtomic(targetPath, data1, 0o644); err != nil {
		t.Fatalf("first WriteFileAtomic error: %v", err)
	}

	read1, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read first file error: %v", err)
	}

	if string(read1) != string(data1) {
		t.Fatalf("first file content = %q, want %q", string(read1), string(data1))
	}

	// A write over an existing target replaces its contents.
	data2 := []byte("version = 2\n")
	if err := atomicfile.WriteFileAtomic(targetPath, data2, 0o644); err != nil {
		t.Fatalf("overwrite WriteFileAtomic error: %v", err)
	}

	read2, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read overwritten file error: %v", err)
	}

	if string(read2) != string(data2) {
		t.Fatalf("overwritten file content = %q, want %q", string(read2), string(data2))
	}
}
