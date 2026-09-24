package strata_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/strata"
)

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

	if _, err := strata.ConfigEditPath(strata.WithAppName("sample")); !errors.Is(err, strata.ErrNoFormats) {
		t.Fatalf("edit path without formats error = %v, want ErrNoFormats", err)
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

	path, err = strata.ConfigEditPath(strata.WithAppName("sample"), strata.WithPath(explicit), strata.WithFormats("toml"))
	if err != nil || path != explicit {
		t.Fatalf("explicit path = %q, %v", path, err)
	}

	if _, err := strata.ConfigEditPath(strata.WithPath(explicit)); !errors.Is(err, strata.ErrNoFormats) {
		t.Fatalf("explicit path without formats error = %v, want ErrNoFormats", err)
	}

	if _, err := strata.ConfigEditPath(strata.WithPath(filepath.Join(base, "config.txt")), strata.WithFormats("toml")); !errors.Is(err, strata.ErrUnsupportedFormat) {
		t.Fatalf("unsupported edit path error = %v, want ErrUnsupportedFormat", err)
	}

	if _, err := strata.ConfigEditPath(strata.WithPath("-")); err == nil || !strings.Contains(err.Error(), "standard input cannot be edited") || errors.Is(err, strata.ErrNoFormats) {
		t.Fatalf("stdin edit path error = %v", err)
	}
}
