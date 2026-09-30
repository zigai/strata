package strata_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/strata"
)

const portKey = "port"

type primitiveConfig struct {
	Port    int
	Timeout time.Duration
	Secret  strata.Secret
}

type typoDatabase struct {
	Port     int
	MaxConns int
}

type typoConfig struct {
	Host     string
	Port     int
	APIKey   string `strata:"api_key,secret"`
	Database typoDatabase
	Labels   map[string]string
}

func (c *typoConfig) SetDefaults() {
	c.Port = 8080
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}

type cascadingConfig struct {
	AppName     string        `toml:"app_name"     yaml:"app_name"`
	AutoClean   bool          `toml:"auto_clean"   yaml:"auto_clean"`
	MaxRetries  int           `toml:"max_retries"  yaml:"max_retries"`
	Timeout     time.Duration `toml:"timeout"      yaml:"timeout"`
	IgnorePaths []string      `toml:"ignore_paths" yaml:"ignore_paths"`
}

func (c *cascadingConfig) SetDefaults() {
	c.AppName = "default-app"
	c.AutoClean = true
	c.MaxRetries = 3
	c.Timeout = 10 * time.Second
	c.IgnorePaths = []string{"/tmp", "/var/tmp"}
}

type formatsTestConfig struct {
	Port int    `json:"port" strata:"port" toml:"port" yaml:"port"`
	Host string `json:"host" strata:"host" toml:"host" yaml:"host"`
}

type narrowConfig struct {
	Small int8    `strata:"small" env:"NARROW_SMALL"`
	Tiny  uint8   `strata:"tiny" env:"NARROW_TINY"`
	Float float32 `strata:"float" env:"NARROW_FLOAT"`
}

type wideConfig struct {
	Small int64 `strata:"small" json:"small" toml:"small" yaml:"small"`
}

type demoConfig struct {
	ServerHost string `json:"server_host" toml:"server_host" yaml:"server_host"`
	ServerPort int    `json:"server_port" toml:"server_port" yaml:"server_port"`
	Debug      bool   `json:"debug"       toml:"debug"       yaml:"debug"`
}

func (d *demoConfig) SetDefaults() {
	d.ServerHost = "127.0.0.1"
	d.ServerPort = 8080
	d.Debug = true
}

// Tests that call isolateLayers cannot run in parallel: it changes the environment.
func isolateLayers(t *testing.T) (string, string) {
	t.Helper()

	systemBase, userBase := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_DIRS", systemBase)
	t.Setenv("XDG_CONFIG_HOME", userBase)
	t.Setenv("HOME", t.TempDir())

	return systemBase, userBase
}

func writeLayerFile(t *testing.T, base, content string) string {
	t.Helper()

	dir := filepath.Join(base, "myapp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	return path
}
