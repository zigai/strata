package strata_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/strata"
)

// Fixtures shared by the tests in this package. A fixture that more than one
// file uses MUST be declared once, here, so every file sees the same type.

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

// narrowConfig carries integer and float fields narrower than the values the
// env tests feed them, so a conversion that wraps or saturates stays observable.
type narrowConfig struct {
	Small int8    `strata:"small" env:"NARROW_SMALL"`
	Tiny  uint8   `strata:"tiny" env:"NARROW_TINY"`
	Float float32 `strata:"float" env:"NARROW_FLOAT"`
}

// wideConfig carries one int64 field, used where a value's exact width matters
// to the provenance and file-size tests.
type wideConfig struct {
	Small int64 `strata:"small" json:"small" toml:"small" yaml:"small"`
}

// demoConfig is the round-trip fixture behind the schema, init, and save tests.
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
