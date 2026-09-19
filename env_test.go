package strata_test

import (
	"errors"
	"testing"

	"github.com/zigai/strata"
)

type numericConfig struct {
	MaxAge uint64  `strata:"max_age"`
	Rate   float64 `strata:"rate"`
}

func TestEnvUnmarshalUintAndFloat(t *testing.T) {
	t.Setenv("NUM_MAX_AGE", "100")
	t.Setenv("NUM_RATE", "3.1415")

	cfg, _, err := strata.Load[numericConfig](
		strata.WithEnvPrefix("NUM_"),
		strata.WithoutFiles(),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.MaxAge != 100 {
		t.Errorf("MaxAge = %d, want 100", cfg.MaxAge)
	}

	if cfg.Rate != 3.1415 {
		t.Errorf("Rate = %f, want 3.1415", cfg.Rate)
	}
}

// A value that does not fit its field MUST be rejected. int8(300) once wrapped
// to 44, and float32(1e40) once saturated to +Inf, both without an error.
func TestEnvRejectsValuesThatDoNotFitTheField(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value string
	}{
		{"int8 too large", "NARROW_SMALL", "300"},
		{"int8 too small", "NARROW_SMALL", "-129"},
		{"uint8 too large", "NARROW_TINY", "300"},
		{"float32 overflows", "NARROW_FLOAT", "1e40"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)

			got, _, err := strata.Load[narrowConfig](strata.WithEnvPrefix("NARROW_"), strata.WithoutFiles())
			if err == nil {
				t.Fatalf("%s = %q loaded as %+v, want an error", tc.key, tc.value, got)
			}

			if !errors.Is(err, strata.ErrInvalidEnvValue) {
				t.Errorf("err = %v, want it to wrap ErrInvalidEnvValue", err)
			}
		})
	}
}

func TestEnvAcceptsValuesAtTheFieldBoundary(t *testing.T) {
	t.Setenv("NARROW_SMALL", "-128")
	t.Setenv("NARROW_TINY", "255")
	t.Setenv("NARROW_FLOAT", "3.5")

	got, _, err := strata.Load[narrowConfig](strata.WithEnvPrefix("NARROW_"), strata.WithoutFiles())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Small != -128 || got.Tiny != 255 || got.Float != 3.5 {
		t.Fatalf("got %+v, want {Small:-128 Tiny:255 Float:3.5}", got)
	}
}

type ptrConfig struct {
	Count *int    `strata:"count"`
	Name  *string `strata:"name"`
	Debug *bool   `strata:"debug"`
}

func TestPrimitivePointerEnv(t *testing.T) {
	t.Setenv("PTR_COUNT", "42")
	t.Setenv("PTR_NAME", "pointer-test")
	t.Setenv("PTR_DEBUG", "true")

	cfg, meta, err := strata.Load[ptrConfig](
		strata.WithEnvPrefix("PTR_"),
		strata.WithoutFiles(),
	)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Count == nil || *cfg.Count != 42 {
		t.Errorf("Count = %v, want 42", cfg.Count)
	}

	if cfg.Name == nil || *cfg.Name != "pointer-test" {
		t.Errorf("Name = %v, want pointer-test", cfg.Name)
	}

	if cfg.Debug == nil || *cfg.Debug != true {
		t.Errorf("Debug = %v, want true", cfg.Debug)
	}

	orig, ok := meta.Where("count")
	if !ok || orig.Source != strata.SourceEnv {
		t.Errorf("meta.Where(count) = %+v, want SourceEnv", orig)
	}
}
