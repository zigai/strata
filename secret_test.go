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

			cfg, meta, err := strata.LoadWithMetadata[primitiveConfig](strata.WithPath(path), strata.WithFormats(ext))
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

	_, meta, err := strata.LoadWithMetadata[secretFileConfig](strata.WithPath(filePath), strata.WithFormats("json"))
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

type redactDatabase struct {
	Password string `strata:"password,secret"`
}

type redactCredentials struct {
	User string `strata:"user"`
	Pass string `strata:"pass"`
}

// redactConfig declares a secret in every supported form.
type redactConfig struct {
	Typed     strata.Secret     `strata:"typed"`
	Pointer   *strata.Secret    `strata:"pointer"`
	Tagged    string            `strata:"tagged,secret"`
	EnvTagged string            `env:"REDACT_ENV_TAGGED,secret" strata:"env_tagged"`
	Database  redactDatabase    `strata:"database"`
	Creds     redactCredentials `strata:"creds,secret"`
}

var redactKeys = []string{"typed", "pointer", "tagged", "env_tagged", "database.password", "creds.user", "creds.pass"}

// Whatever layer sets a secret key, its origin, from Where and from Origins,
// holds [REDACTED] and never the value. Each layer uses its own literal, so a
// leak cannot hide behind another layer's value.
func TestSecretOriginsAreRedactedInEveryLayer(t *testing.T) {
	const leak = "leak-7f3a"

	toml := "typed = 'toml-" + leak + "'\npointer = 'toml-" + leak + "'\ntagged = 'toml-" + leak + "'\nenv_tagged = 'toml-" + leak +
		"'\n[database]\npassword = 'toml-" + leak + "'\n[creds]\nuser = 'toml-" + leak + "'\npass = 'toml-" + leak + "'\n"
	yaml := "typed: yaml-" + leak + "\npointer: yaml-" + leak + "\ntagged: yaml-" + leak + "\nenv_tagged: yaml-" + leak +
		"\ndatabase:\n  password: yaml-" + leak + "\ncreds:\n  user: yaml-" + leak + "\n  pass: yaml-" + leak + "\n"
	json := `{"typed": "json-` + leak + `", "pointer": "json-` + leak + `", "tagged": "json-` + leak + `", "env_tagged": "json-` + leak +
		`", "database": {"password": "json-` + leak + `"}, "creds": {"user": "json-` + leak + `", "pass": "json-` + leak + `"}}`
	pointerDefault := strata.Secret("default-" + leak)

	for _, tc := range []struct {
		name   string
		source strata.SourceKind
		opts   func(t *testing.T) []strata.Option
	}{
		{"default", strata.SourceDefault, func(*testing.T) []strata.Option {
			v := "default-" + leak

			return []strata.Option{strata.WithDefaults(redactConfig{
				Typed: strata.Secret(v), Pointer: &pointerDefault, Tagged: v, EnvTagged: v,
				Database: redactDatabase{Password: v}, Creds: redactCredentials{User: v, Pass: v},
			})}
		}},
		{"toml", strata.SourceFile, func(t *testing.T) []strata.Option {
			t.Helper()

			return []strata.Option{strata.WithPath(writeFile(t, "c.toml", toml)), strata.WithFormats("toml")}
		}},
		{"yaml", strata.SourceFile, func(t *testing.T) []strata.Option {
			t.Helper()

			return []strata.Option{strata.WithPath(writeFile(t, "c.yaml", yaml)), strata.WithFormats("yaml")}
		}},
		{"json", strata.SourceFile, func(t *testing.T) []strata.Option {
			t.Helper()

			return []strata.Option{strata.WithPath(writeFile(t, "c.json", json)), strata.WithFormats("json")}
		}},
		{"stdin", strata.SourceStdin, func(*testing.T) []strata.Option {
			return []strata.Option{strata.WithPath("-"), strata.WithStdin(strings.NewReader(toml)), strata.WithFormats("toml")}
		}},
		{"env", strata.SourceEnv, func(t *testing.T) []strata.Option {
			t.Helper()

			for _, name := range []string{"REDACT_TYPED", "REDACT_POINTER", "REDACT_TAGGED", "REDACT_ENV_TAGGED", "REDACT_DATABASE_PASSWORD", "REDACT_CREDS_USER", "REDACT_CREDS_PASS"} {
				t.Setenv(name, "env-"+leak)
			}

			return []strata.Option{strata.WithEnvPrefix("REDACT_")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, meta, err := strata.LoadWithMetadata[redactConfig](tc.opts(t)...)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if !strings.Contains(cfg.Creds.Pass, leak) {
				t.Fatalf("creds.pass = %q: the layer did not set the value", cfg.Creds.Pass)
			}

			for _, key := range redactKeys {
				origin, ok := meta.Where(key)
				if !ok || origin.Source != tc.source || origin.RawValue != "[REDACTED]" {
					t.Errorf("Where(%s) = %+v, %t; want %s with [REDACTED]", key, origin, ok, tc.source)
				}
			}

			for _, origin := range meta.Origins() {
				if strings.Contains(origin.RawValue, leak) {
					t.Errorf("Origins() leaks %s: %+v", origin.Key, origin)
				}
			}
		})
	}
}
