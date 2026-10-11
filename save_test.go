package strata_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zigai/strata"
)

func TestSaveYAMLPreservesLeadingNewline(t *testing.T) {
	t.Parallel()

	type config struct{ Text string }

	want := config{Text: "\na"}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := strata.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats("yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got != want {
		t.Fatalf("saved configuration = %+v, want %+v", got, want)
	}
}

func TestSaveYAMLPreservesLeadingNewlineInMapKeys(t *testing.T) {
	t.Parallel()

	type config struct{ Values map[string]int }

	want := config{Values: map[string]int{"\na": 1}}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := strata.Save(path, want); err != nil {
		t.Fatal(err)
	}

	got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats("yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("saved configuration = %+v, want %+v", got, want)
	}
}

func TestSaveJSONPreservesTaggedEmbeddedFields(t *testing.T) {
	t.Parallel()

	type DB struct{ Port int }

	type config struct {
		DB `json:"db"`
	}

	want := config{Port: 9000}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := strata.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := strata.Load[config](strata.WithPath(path), strata.WithFormats("json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got != want {
		t.Fatalf("saved configuration = %+v, want %+v", got, want)
	}
}

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

	loaded, err := strata.Load[demoConfig](strata.WithPath(p), strata.WithFormats("toml"))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if loaded.ServerHost != "192.168.1.100" || loaded.ServerPort != 9090 || loaded.Debug != false {
		t.Fatalf("Loaded saved mismatch: %+v", loaded)
	}
}

type savedNode struct {
	HTTPPort int
	Timeout  time.Duration
	Next     *savedNode
}

func TestSaveReloadsRecursiveConfigurationKeys(t *testing.T) {
	t.Parallel()

	want := savedNode{HTTPPort: 8080, Timeout: 2 * time.Second, Next: &savedNode{HTTPPort: 9000, Timeout: time.Second}}

	for _, format := range []string{jsonFormat, yamlFormat, tomlFormat} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config."+format)
			if err := strata.Save(path, want); err != nil {
				t.Fatalf("Save: %v", err)
			}

			got, err := strata.Load[savedNode](strata.WithPath(path), strata.WithFormats(format))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("saved configuration = %+v, want %+v", got, want)
			}
		})
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

		if err := strata.Set[narrowConfig](path, "small", 2); err != nil {
			t.Fatalf("Set: %v", err)
		}

		if got := fileMode(t, path); got != want {
			t.Errorf("Set on a %#o file left %#o, want the mode preserved", want, got)
		}
	}
}
