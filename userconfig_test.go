package strata_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/strata"
)

// With no file in the user directory, UserConfigFile names config.toml there.
func TestUserConfigFileDefaultsToTOML(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	path, err := strata.UserConfigFile("myapp")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(path, "myapp/config.toml") {
		t.Fatalf("path = %s", path)
	}
}

func TestConfigEditPathUsesLoadingOptions(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)

	appDir := filepath.Join(base, "sample")
	if err := os.MkdirAll(appDir, 0o750); err != nil {
		t.Fatal(err)
	}

	yamlPath := filepath.Join(appDir, "config.yaml")
	if err := os.WriteFile(yamlPath, []byte("port: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := strata.ConfigEditPath(strata.WithAppName("sample"), strata.WithFormats("toml"))
	if err != nil || path != filepath.Join(appDir, "config.toml") {
		t.Fatalf("TOML path = %q, %v", path, err)
	}

	path, err = strata.ConfigEditPath(strata.WithAppName("sample"), strata.WithFormats("yaml"))
	if err != nil || path != yamlPath {
		t.Fatalf("YAML path = %q, %v", path, err)
	}

	explicit := filepath.Join(base, "explicit.toml")

	path, err = strata.ConfigEditPath(strata.WithAppName("sample"), strata.WithPath(explicit))
	if err != nil || path != explicit {
		t.Fatalf("explicit path = %q, %v", path, err)
	}
}
