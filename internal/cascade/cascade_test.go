package cascade_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/strata/internal/cascade"
)

func TestCascadeDiscovery(t *testing.T) {
	t.Parallel()

	t.Run("no config bypasses all", func(t *testing.T) {
		t.Parallel()

		layers, err := cascade.Discover(cascade.Params{
			AppName:      "testapp",
			WithoutFiles: true,
			Extensions:   []string{".toml", ".yaml"},
		})
		if err != nil {
			t.Fatalf("Discover error: %v", err)
		}

		if len(layers) != 0 {
			t.Fatalf("layers = %v, want empty", layers)
		}
	})

	t.Run("explicit path loads exact file", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()

		filePath := filepath.Join(tmpDir, "custom.toml")
		if err := os.WriteFile(filePath, []byte("host = \"explicit-host\"\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		layers, err := cascade.Discover(cascade.Params{
			ExplicitPath: filePath,
			Extensions:   []string{".toml"},
		})
		if err != nil {
			t.Fatalf("Discover error: %v", err)
		}

		if len(layers) != 1 || layers[0].Path != filePath {
			t.Fatalf("layers = %v, want [%s]", layers, filePath)
		}
	})

	t.Run("explicit path stdin reads stream", func(t *testing.T) {
		t.Parallel()

		buf := bytes.NewBufferString("host = \"stdin-host\"\nport = 9000\n")

		layers, err := cascade.Discover(cascade.Params{
			ExplicitPath: "-",
			StdinReader:  buf,
			MaxFileSize:  1024,
			Extensions:   []string{".toml"},
		})
		if err != nil {
			t.Fatalf("Discover error: %v", err)
		}

		if len(layers) != 1 || layers[0].Source != cascade.SourceStdin {
			t.Fatalf("layers = %+v, want 1 stdin layer", layers)
		}

		if string(layers[0].Data) != "host = \"stdin-host\"\nport = 9000\n" {
			t.Fatalf("stdin data = %q", string(layers[0].Data))
		}
	})
}

func TestCascadeUserTierDiscovery(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("skipping Unix/macOS user tier test on Windows")
	}

	t.Run("discovers config in XDG_CONFIG_HOME", func(t *testing.T) {
		xdgDir := t.TempDir()

		appDir := filepath.Join(xdgDir, "testapp")
		if err := os.MkdirAll(appDir, 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}

		configPath := filepath.Join(appDir, "config.toml")
		if err := os.WriteFile(configPath, []byte("host = \"xdg-host\"\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		t.Setenv("XDG_CONFIG_HOME", xdgDir)

		layers, err := cascade.Discover(cascade.Params{
			AppName:    "testapp",
			Extensions: []string{".toml"},
		})
		if err != nil {
			t.Fatalf("Discover error: %v", err)
		}

		if len(layers) != 1 || layers[0].Source != cascade.SourceUser {
			t.Fatalf("layers = %+v, want 1 user layer", layers)
		}

		if layers[0].Path != configPath {
			t.Fatalf("Path = %q, want %q", layers[0].Path, configPath)
		}
	})

	t.Run("discovers config in dot config when XDG_CONFIG_HOME is unset", func(t *testing.T) {
		homeDir := t.TempDir()

		appDir := filepath.Join(homeDir, ".config", "testapp")
		if err := os.MkdirAll(appDir, 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}

		configPath := filepath.Join(appDir, "config.toml")
		if err := os.WriteFile(configPath, []byte("host = \"dot-config-host\"\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", homeDir)

		layers, err := cascade.Discover(cascade.Params{
			AppName:    "testapp",
			Extensions: []string{".toml"},
		})
		if err != nil {
			t.Fatalf("Discover error: %v", err)
		}

		if len(layers) != 1 || layers[0].Source != cascade.SourceUser {
			t.Fatalf("layers = %+v, want 1 user layer", layers)
		}

		if layers[0].Path != configPath {
			t.Fatalf("Path = %q, want %q", layers[0].Path, configPath)
		}
	})
}
