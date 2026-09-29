package strata_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"
)

type numericConfig struct {
	MaxAge uint64  `strata:"max_age"`
	Rate   float64 `strata:"rate"`
}

func TestEnvUnmarshalUintAndFloat(t *testing.T) {
	t.Setenv("NUM_MAX_AGE", "100")
	t.Setenv("NUM_RATE", "3.1415")

	cfg, err := strata.Load[numericConfig](
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

			got, err := strata.Load[narrowConfig](strata.WithEnvPrefix("NARROW_"), strata.WithoutFiles())
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

	got, err := strata.Load[narrowConfig](strata.WithEnvPrefix("NARROW_"), strata.WithoutFiles())
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

	cfg, meta, err := strata.LoadWithMetadata[ptrConfig](
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

func TestEnvironmentNeedsPrefixForDerivedNames(t *testing.T) {
	t.Setenv("PORT", "1234")
	t.Setenv("HOST", "from-env")
	t.Setenv("TAGGED_TOKEN", "abc")

	type config struct {
		Host  string
		Port  int
		Token string `env:"TAGGED_TOKEN"`
	}

	cfg, err := strata.Load[config]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Host != "" || cfg.Port != 0 {
		t.Errorf("cfg = %+v, want PORT and HOST ignored without a prefix", cfg)
	}

	if cfg.Token != "abc" {
		t.Errorf("Token = %q, want the explicitly tagged variable to bind", cfg.Token)
	}
}

func TestEnvVars(t *testing.T) {
	t.Parallel()

	vars := strata.EnvVars[typoConfig]("myapp")

	got := make(map[string]strata.EnvVar)
	for _, v := range vars {
		got[v.Key] = v
	}

	tests := []struct {
		key    string
		name   string
		secret bool
	}{
		{key: "host", name: "MYAPP_HOST"},
		{key: "api_key", name: "MYAPP_API_KEY", secret: true},
		{key: "database.max_conns", name: "MYAPP_DATABASE_MAX_CONNS"},
	}

	for _, tt := range tests {
		v, ok := got[tt.key]
		if !ok {
			t.Fatalf("no variable for %s in %+v", tt.key, vars)
		}

		if v.Name != tt.name || v.Secret != tt.secret {
			t.Errorf("%s = %+v, want name %s secret %t", tt.key, v, tt.name, tt.secret)
		}
	}

	if _, ok := got["labels"]; ok {
		t.Error("a map field is listed, but the environment cannot set one")
	}

	if names := got["database.port"].Names; !slices.Equal(names, []string{"MYAPP_DATABASE__PORT", "MYAPP_DATABASE_PORT"}) {
		t.Errorf("database.port names = %v", names)
	}

	if unprefixed := strata.EnvVars[typoConfig](""); len(unprefixed) != 0 {
		t.Errorf("EnvVars without a prefix = %+v, want none for untagged fields", unprefixed)
	}
}

type envDecodeConfig struct {
	Flag  bool            `strata:"flag"`
	Wait  time.Duration   `strata:"wait"`
	Span  strata.Duration `strata:"span"`
	Tags  []string        `strata:"tags"`
	Level editLevel       `strata:"level"`
}

func TestEnvDecodesBoolSpellings(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"1", true},
		{"t", true},
		{"true", true},
		{"yes", true},
		{"y", true},
		{"on", true},
		{"TRUE", true},
		{"Yes", true},
		{" on ", true},
		{"0", false},
		{"f", false},
		{"false", false},
		{"no", false},
		{"n", false},
		{"off", false},
		{"FALSE", false},
		{"Off", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("DEC_FLAG", tc.value)

			got, err := strata.Load[envDecodeConfig](strata.WithDefaults(envDecodeConfig{Flag: !tc.want}), strata.WithEnvPrefix("DEC_"))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if got.Flag != tc.want {
				t.Fatalf("DEC_FLAG=%q loaded as %t, want %t", tc.value, got.Flag, tc.want)
			}
		})
	}
}

// Durations accept days and weeks from the environment, lists split on
// commas, and a type with UnmarshalText decodes through it.
func TestEnvDecodesByFieldType(t *testing.T) {
	t.Setenv("DEC_WAIT", "7d")
	t.Setenv("DEC_SPAN", "2w")
	t.Setenv("DEC_TAGS", "a,b")
	t.Setenv("DEC_LEVEL", "debug")

	got, err := strata.Load[envDecodeConfig](strata.WithEnvPrefix("DEC_"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := envDecodeConfig{Wait: 168 * time.Hour, Span: strata.Duration(336 * time.Hour), Tags: []string{"a", "b"}, Level: "debug"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// An env value that does not decode fails with ErrInvalidEnvValue, and the
// message names both the key and the variable so the user can find it.
func TestEnvErrorsNameTheKeyAndVariable(t *testing.T) {
	for _, tc := range []struct {
		name, variable, value, key string
	}{
		{"bool", "DEC_FLAG", "maybe", "flag"},
		{"duration", "DEC_WAIT", "5x", "wait"},
		{"text decoder", "DEC_LEVEL", "bogus", "level"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.variable, tc.value)

			_, err := strata.Load[envDecodeConfig](strata.WithEnvPrefix("DEC_"))
			if !errors.Is(err, strata.ErrInvalidEnvValue) {
				t.Fatalf("err = %v, want ErrInvalidEnvValue", err)
			}

			if msg := err.Error(); !strings.Contains(msg, tc.variable) || !strings.Contains(msg, `"`+tc.key+`"`) && !strings.Contains(msg, " "+tc.key) {
				t.Errorf("err = %q, want it to name %s and key %s", msg, tc.variable, tc.key)
			}
		})
	}
}

// EnvVars lists every variable in declaration order, and each listed name
// really sets its key when fed back into a load.
func TestEnvVarsMatchesWhatLoadReads(t *testing.T) {
	want := []strata.EnvVar{
		{Key: "host", Name: "MYAPP_HOST", Names: []string{"MYAPP_HOST"}},
		{Key: portKey, Name: "MYAPP_PORT", Names: []string{"MYAPP_PORT"}},
		{Key: "api_key", Name: "MYAPP_API_KEY", Names: []string{"MYAPP_API_KEY"}, Secret: true},
		{Key: "database.port", Name: "MYAPP_DATABASE_PORT", Names: []string{"MYAPP_DATABASE__PORT", "MYAPP_DATABASE_PORT"}},
		{Key: "database.max_conns", Name: "MYAPP_DATABASE_MAX_CONNS", Names: []string{"MYAPP_DATABASE__MAX_CONNS", "MYAPP_DATABASE_MAX_CONNS"}},
	}

	vars := strata.EnvVars[typoConfig]("myapp")
	if !reflect.DeepEqual(vars, want) {
		t.Fatalf("EnvVars =\n%+v\nwant\n%+v", vars, want)
	}

	values := map[string]string{"host": "h", portKey: "1", "api_key": "k", "database.port": "2", "database.max_conns": "3"}
	wantCfg := typoConfig{Host: "h", Port: 1, APIKey: "k", Database: typoDatabase{Port: 2, MaxConns: 3}}

	for _, tc := range []struct {
		name string
		pick func(strata.EnvVar) string
	}{
		{"documented Name", func(v strata.EnvVar) string { return v.Name }},
		{"first of Names", func(v strata.EnvVar) string { return v.Names[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range vars {
				t.Setenv(tc.pick(v), values[v.Key])
			}

			got, err := strata.Load[typoConfig](strata.WithEnvPrefix("myapp"))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if !reflect.DeepEqual(got, wantCfg) {
				t.Fatalf("got %+v, want %+v", got, wantCfg)
			}
		})
	}
}
