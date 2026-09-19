package strata_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/strata"
)

func TestSave(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "saved.toml")

	cfg := demoConfig{
		ServerHost: "192.168.1.100",
		ServerPort: 9090,
		Debug:      false,
	}

	if err := strata.Save(p, cfg, strata.WithFileMode(0o600)); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded, _, err := strata.Load[demoConfig](strata.WithExplicitPath(p))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if loaded.ServerHost != "192.168.1.100" || loaded.ServerPort != 9090 || loaded.Debug != false {
		t.Fatalf("Loaded saved mismatch: %+v", loaded)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	return info.Mode().Perm()
}

// Set MUST keep the permission bits of the file it edits, and Save MUST apply the
// mode it was given. Set once forced 0o644 on every rewrite, and Save's default
// was never applied because the temporary file's 0o600 survived the rename.
func TestFileModesSurviveWrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	saved := filepath.Join(dir, "saved.toml")
	if err := strata.Save(saved, narrowConfig{}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got := fileMode(t, saved); got != 0o644 {
		t.Errorf("Save default mode = %#o, want 0644", got)
	}

	for _, want := range []os.FileMode{0o600, 0o640, 0o400, 0o664} {
		// A distinct path per case. A 0400 mode would otherwise block the next
		// seed write.
		path := filepath.Join(dir, fmt.Sprintf("cfg-%#o.toml", want))

		if err := os.WriteFile(path, []byte("small = 1\n"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}

		if err := os.Chmod(path, want); err != nil {
			t.Fatalf("chmod: %v", err)
		}

		if err := strata.Set(path, "small", 2); err != nil {
			t.Fatalf("Set: %v", err)
		}

		if got := fileMode(t, path); got != want {
			t.Errorf("Set on a %#o file left %#o, want the mode preserved", want, got)
		}
	}
}
