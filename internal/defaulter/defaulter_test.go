package defaulter_test

import (
	"errors"
	"testing"
	"time"

	"github.com/zigai/strata/internal/defaulter"
)

type serverConfig struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

func (s *serverConfig) SetDefaults() {
	s.Host = "127.0.0.1"
	s.Port = 8080
}

type databaseConfig struct {
	MaxConns int           `strata:"max_conns"`
	Timeout  time.Duration `strata:"timeout"`
}

func (d *databaseConfig) SetDefaults() {
	d.MaxConns = 10
	d.Timeout = 5 * time.Second
}

type appConfig struct {
	AppName  string          `strata:"app_name"`
	Server   serverConfig    `strata:"server"`
	Database *databaseConfig `strata:"database"`
	Debug    bool            `strata:"debug"`
}

func (a *appConfig) SetDefaults() {
	a.AppName = "default-app"
	a.Debug = true
}

func TestApplyDefaults(t *testing.T) {
	t.Parallel()

	t.Run("non pointer returns error", func(t *testing.T) {
		t.Parallel()

		var cfg appConfig

		err := defaulter.Apply(cfg, nil)
		if !errors.Is(err, defaulter.ErrTargetNotPointer) {
			t.Fatalf("expected ErrTargetNotPointer, got %v", err)
		}
	})

	t.Run("nil pointer returns error", func(t *testing.T) {
		t.Parallel()

		var cfg *appConfig

		err := defaulter.Apply(cfg, nil)
		if !errors.Is(err, defaulter.ErrTargetNotPointer) {
			t.Fatalf("expected ErrTargetNotPointer, got %v", err)
		}
	})

	t.Run("applies defaults recursively and records origins", func(t *testing.T) {
		t.Parallel()

		var cfg appConfig

		recorded := make(map[string]string)

		recordFn := func(key, val string) {
			recorded[key] = val
		}

		if err := defaulter.Apply(&cfg, recordFn); err != nil {
			t.Fatalf("ApplyDefaults error: %v", err)
		}

		if cfg.AppName != "default-app" {
			t.Errorf("AppName = %q, want %q", cfg.AppName, "default-app")
		}

		if !cfg.Debug {
			t.Errorf("Debug = false, want true")
		}

		if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 8080 {
			t.Errorf("Server = %+v, want 127.0.0.1:8080", cfg.Server)
		}

		if cfg.Database == nil || cfg.Database.MaxConns != 10 || cfg.Database.Timeout != 5*time.Second {
			t.Errorf("Database = %+v, want maxConns 10, timeout 5s", cfg.Database)
		}

		if val, ok := recorded["app_name"]; !ok || val != "default-app" {
			t.Errorf("recorded app_name = %v, want default-app", val)
		}

		if val, ok := recorded["server.port"]; !ok || val != "8080" {
			t.Errorf("recorded server.port = %v, want 8080", val)
		}
	})
}

type InnerConfig struct {
	Host string `strata:"host"`
}

func (i *InnerConfig) SetDefaults() {
	i.Host = "127.0.0.1"
}

type OuterConfig struct {
	*InnerConfig

	Port int `strata:"port"`
}

func TestEmbeddedNilPointerDefaulter(t *testing.T) {
	t.Parallel()

	var cfg OuterConfig
	if err := defaulter.Apply(&cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.InnerConfig == nil || cfg.Host != "127.0.0.1" {
		t.Errorf("Host = %v, want 127.0.0.1", cfg.Host)
	}
}

type nestedOuterConfig struct {
	Outer OuterConfig `strata:"outer"`
}

func TestNestedEmbeddedNilPointerDefaulter(t *testing.T) {
	t.Parallel()

	var cfg nestedOuterConfig
	if err := defaulter.Apply(&cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Outer.InnerConfig == nil || cfg.Outer.Host != "127.0.0.1" {
		t.Errorf("Outer.Host = %v, want 127.0.0.1", cfg.Outer.Host)
	}
}

type CountingEmbeddedDefaulter struct{ Calls int }

func (e *CountingEmbeddedDefaulter) SetDefaults() { e.Calls++ }

func TestEmbeddedDefaulterCalledOnce(t *testing.T) {
	t.Parallel()

	cfg := struct{ CountingEmbeddedDefaulter }{}
	if err := defaulter.Apply(&cfg, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Calls != 1 {
		t.Errorf("SetDefaults called %d times on the same embedded object, want 1", cfg.Calls)
	}
}
