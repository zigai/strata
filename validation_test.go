package strata_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/strata"
)

type attributedConfig struct {
	Port int    `strata:"port" yaml:"port"`
	Host string `strata:"host" yaml:"host"`
}

// ValidateWith reports the first bad key it finds.
func (c *attributedConfig) ValidateWith(meta *strata.Metadata) error {
	if c.Port > 9000 {
		return meta.NewConfigError("port", errors.New("above the privileged ceiling"))
	}

	return nil
}

type crossFieldConfig struct {
	Min int `strata:"min" yaml:"min"`
	Max int `strata:"max" yaml:"max"`
}

// ValidateWith reports a failure spanning two keys, which has no single key to
// attribute it to.
func (c *crossFieldConfig) ValidateWith(*strata.Metadata) error {
	if c.Min > c.Max {
		return errors.New("min must not exceed max")
	}

	return nil
}

type multiFailureConfig struct {
	Port int    `strata:"port" yaml:"port"`
	Host string `strata:"host" yaml:"host"`
}

// ValidateWith reports every problem in one pass.
func (c *multiFailureConfig) ValidateWith(meta *strata.Metadata) error {
	var errs []error

	if c.Port > 9000 {
		errs = append(errs, meta.NewConfigError("port", errors.New("above the privileged ceiling")))
	}

	if c.Host == "" {
		errs = append(errs, meta.NewConfigError("host", errors.New("host is required")))
	}

	return errors.Join(errs...)
}

// bothConfig implements both validation interfaces, to pin which one runs.
type bothConfig struct {
	Port int `strata:"port" yaml:"port"`
}

func (c *bothConfig) Validate() error {
	return errors.New("plain Validate ran")
}

func (c *bothConfig) ValidateWith(meta *strata.Metadata) error {
	return meta.NewConfigError("port", errors.New("ValidateWith ran"))
}

// plainConfig implements Validator only. That interface cannot name a key.
type plainConfig struct {
	Port int `strata:"port" yaml:"port"`
}

func (c *plainConfig) Validate() error {
	return errors.New("port is unacceptable")
}

func loadFromYAML[T any](t *testing.T, body string) error {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, _, err := strata.Load[T](strata.WithExplicitPath(path))

	return err
}

// A MetadataValidator receives the key, the raw input, and the position the value
// came from, which is what makes the ConfigError documentation true.
func TestMetadataValidatorAttributesTheFailure(t *testing.T) {
	t.Parallel()

	err := loadFromYAML[attributedConfig](t, "# lead\nport: 9090\nhost: localhost\n")
	if err == nil {
		t.Fatal("expected a validation failure")
	}

	got := err.Error()
	t.Logf("rendered:\n%s", got)

	for _, want := range []string{
		"above the privileged ceiling",
		`for port: "9090"`,
		"--> set by ",
		":2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("error text missing %q:\n%s", want, got)
		}
	}
}

// A failure that spans fields has no key to name, so it MUST render the message
// alone rather than a wrong key.
func TestMetadataValidatorCrossFieldFailureHasNoKey(t *testing.T) {
	t.Parallel()

	err := loadFromYAML[crossFieldConfig](t, "min: 10\nmax: 1\n")
	if err == nil {
		t.Fatal("expected a validation failure")
	}

	got := err.Error()
	t.Logf("rendered: %s", got)

	if strings.Contains(got, "-->") {
		t.Errorf("unattributable failure should not claim an origin:\n%s", got)
	}

	if !strings.Contains(got, "min must not exceed max") {
		t.Errorf("error text lost the message: %s", got)
	}

	if !strings.Contains(got, "config error:") {
		t.Errorf("failure should still be a ConfigError: %s", got)
	}
}

// Every reported key MUST stay reachable. A single-keyed error type could not
// express this.
func TestMetadataValidatorReportsEveryFailure(t *testing.T) {
	t.Parallel()

	err := loadFromYAML[multiFailureConfig](t, "port: 9090\nhost: \"\"\n")
	if err == nil {
		t.Fatal("expected a validation failure")
	}

	got := err.Error()
	t.Logf("rendered:\n%s", got)

	for _, want := range []string{"for port", "for host", "above the privileged ceiling", "host is required"} {
		if !strings.Contains(got, want) {
			t.Errorf("error text missing %q:\n%s", want, got)
		}
	}

	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("err is %T, want a joined error", err)
	}

	if len(joined.Unwrap()) != 2 {
		t.Errorf("joined %d failures, want 2", len(joined.Unwrap()))
	}
}

// Validator remains the smaller interface. Its failures are reported as a
// ConfigError with no key to name.
func TestPlainValidatorIsUnattributed(t *testing.T) {
	t.Parallel()

	err := loadFromYAML[plainConfig](t, "port: 9090\n")
	if err == nil {
		t.Fatal("expected a validation failure")
	}

	got := err.Error()
	t.Logf("rendered: %s", got)

	if !strings.Contains(got, "port is unacceptable") {
		t.Errorf("error text lost the message: %s", got)
	}

	if strings.Contains(got, "-->") || strings.Contains(got, "for port") {
		t.Errorf("a plain Validator cannot attribute a key:\n%s", got)
	}

	if _, ok := errors.AsType[*strata.ConfigError](err); !ok {
		t.Errorf("err is %T, want it to stay an *strata.ConfigError", err)
	}
}

// A type implementing both MUST be validated by the one that can attribute.
func TestMetadataValidatorTakesPrecedence(t *testing.T) {
	t.Parallel()

	err := loadFromYAML[bothConfig](t, "port: 9090\n")
	if err == nil {
		t.Fatal("expected a validation failure")
	}

	got := err.Error()
	t.Logf("rendered: %s", got)

	if !strings.Contains(got, "ValidateWith ran") {
		t.Errorf("ValidateWith did not run:\n%s", got)
	}

	if strings.Contains(got, "plain Validate ran") {
		t.Errorf("Validate ran despite the type also implementing MetadataValidator:\n%s", got)
	}
}

type validatedConfig struct {
	Port int `strata:"port"`
}

func (v *validatedConfig) Validate() error {
	if v.Port < 1024 {
		return errors.New("port must be >= 1024")
	}

	return nil
}

func TestValidatorPostLoadHook(t *testing.T) {
	t.Parallel()

	t.Run("fails when invalid", func(t *testing.T) {
		t.Parallel()

		cfg := validatedConfig{Port: 80}

		_, _, err := strata.Load[validatedConfig](
			strata.WithoutFiles(),
			strata.WithDefaults(cfg),
		)
		if err == nil {
			t.Fatalf("expected validation error, got nil")
		}

		if !strings.Contains(err.Error(), "port must be >= 1024") {
			t.Errorf("error = %v, want 'port must be >= 1024'", err)
		}
	})

	t.Run("succeeds when valid", func(t *testing.T) {
		t.Parallel()

		cfg := validatedConfig{Port: 8080}

		loaded, _, err := strata.Load[validatedConfig](
			strata.WithoutFiles(),
			strata.WithDefaults(cfg),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if loaded.Port != 8080 {
			t.Errorf("Port = %d, want 8080", loaded.Port)
		}
	})
}
