package strata_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"
)

type secretConfig struct {
	Token string `strata:"token,secret" env:"SECRET_TOKEN"`
}

func (s *secretConfig) SetDefaults() {
	s.Token = "default-insecure-token"
}

type secretFileConfig struct {
	Token string `strata:"token,secret" json:"token"`
}

func (s *secretFileConfig) SetDefaults() {
	s.Token = "default-secret"
}

// Secret hides its value from fmt and slog, and Value returns it.
func TestSecretRedactsWhenPrinted(t *testing.T) {
	secret := strata.Secret("private-value")
	if secret.String() != "[REDACTED]" || secret.GoString() != "[REDACTED]" || secret.LogValue().String() != "[REDACTED]" || secret.Value() != "private-value" {
		t.Fatal("secret methods")
	}
}

// A Secret and a plain [time.Duration] decode from every format; the secret's
// origin is redacted, and Save writes its real value.
func TestSecretAndDurationRoundTripEveryFormat(t *testing.T) {
	secret := strata.Secret("private-value")

	for ext, data := range map[string]string{
		".toml": "timeout = '45s'\nsecret = 'private-value'\n",
		".json": `{"timeout":"45s","secret":"private-value"}`,
		".yaml": "timeout: 45s\nsecret: private-value\n",
	} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config"+ext)
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, meta, err := strata.LoadWithMetadata[primitiveConfig](strata.WithPath(path))
			if err != nil {
				t.Fatal(err)
			}

			if cfg.Timeout != 45*time.Second || cfg.Secret.Value() != "private-value" {
				t.Fatalf("config = %+v", cfg)
			}

			if origin, _ := meta.Where("secret"); origin.RawValue != "[REDACTED]" {
				t.Fatalf("origin = %+v", origin)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "saved.json")
	if err := strata.Save(path, primitiveConfig{Secret: secret, Timeout: 45 * time.Second}); err != nil {
		t.Fatal(err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(saved), "private-value") || !strings.Contains(string(saved), "45s") {
		t.Fatalf("saved file = %s", saved)
	}
}

func TestSecretTagMasking(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "super-secret-api-key")

	_, meta, err := strata.LoadWithMetadata[secretConfig](
		strata.WithoutFiles(),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	orig, ok := meta.Where("token")
	if !ok {
		t.Fatalf("Where(token) not found")
	}

	if orig.RawValue != "[REDACTED]" {
		t.Errorf("RawValue = %q, want [REDACTED]", orig.RawValue)
	}
}

func TestFileSecretRedaction(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	filePath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(filePath, []byte(`{"token":"file-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, meta, err := strata.LoadWithMetadata[secretFileConfig](strata.WithPath(filePath))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	o, ok := meta.Where("token")
	if !ok {
		t.Fatal("missing origin")
	}

	if o.RawValue != "[REDACTED]" {
		t.Errorf("secret file origin leaked %q", o.RawValue)
	}

	msg := meta.NewConfigError("token", errors.New("invalid token")).Error()
	if strings.Contains(msg, "file-secret") {
		t.Errorf("validation error leaked file secret:\n%s", msg)
	}
}
