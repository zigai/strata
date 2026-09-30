package cascade_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zigai/strata/internal/cascade"
)

func TestCascadeDiscovery(t *testing.T) {
	t.Parallel()

	t.Run("no config bypasses all", func(t *testing.T) {
		t.Parallel()

		layers, err := cascade.Discover(cascade.DiscoverOptions{
			AppName:       "testapp",
			SkipDiscovery: true,
			Extensions:    []string{".toml", ".yaml"},
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

		layers, err := cascade.Discover(cascade.DiscoverOptions{
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

		layers, err := cascade.Discover(cascade.DiscoverOptions{
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

func TestCascadeUserLayerDiscovery(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("skipping Unix/macOS user layer test on Windows")
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
		t.Setenv("XDG_CONFIG_DIRS", t.TempDir())

		layers, err := cascade.Discover(cascade.DiscoverOptions{
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
		t.Setenv("XDG_CONFIG_DIRS", t.TempDir())
		t.Setenv("HOME", homeDir)

		layers, err := cascade.Discover(cascade.DiscoverOptions{
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

func isolateLayers(t *testing.T) (string, string) {
	t.Helper()

	systemBase, userBase := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_DIRS", systemBase)
	t.Setenv("XDG_CONFIG_HOME", userBase)
	t.Setenv("HOME", t.TempDir())

	return systemBase, userBase
}

func writeLayerFile(t *testing.T, base, name string) string {
	t.Helper()

	dir := filepath.Join(base, "testapp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("host = \"tier\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return path
}

func TestDiscoverOrdersSystemBelowUser(t *testing.T) {
	systemBase, userBase := isolateLayers(t)
	systemPath := writeLayerFile(t, systemBase, "config.toml")
	userPath := writeLayerFile(t, userBase, "config.toml")

	layers, err := cascade.Discover(cascade.DiscoverOptions{AppName: "testapp", Extensions: []string{".toml"}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	want := []cascade.Layer{
		{Source: cascade.SourceSystem, Path: systemPath},
		{Source: cascade.SourceUser, Path: userPath},
	}
	if !reflect.DeepEqual(layers, want) {
		t.Fatalf("layers = %+v, want %+v", layers, want)
	}
}

func TestDiscoverSystemLayerSearchesConfigDirsInOrder(t *testing.T) {
	_, _ = isolateLayers(t)

	withoutFile, first, second := t.TempDir(), t.TempDir(), t.TempDir()
	firstPath := writeLayerFile(t, first, "config.toml")
	writeLayerFile(t, second, "config.toml")

	t.Setenv("XDG_CONFIG_DIRS", withoutFile+": :"+first+":"+second)

	layers, err := cascade.Discover(cascade.DiscoverOptions{AppName: "testapp", Extensions: []string{".toml"}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	want := []cascade.Layer{{Source: cascade.SourceSystem, Path: firstPath}}
	if !reflect.DeepEqual(layers, want) {
		t.Fatalf("layers = %+v, want %+v", layers, want)
	}
}
