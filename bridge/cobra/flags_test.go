package stratacobra_test

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/strata"
	stratacobra "github.com/zigai/strata/bridge/cobra"
)

const commandName = "test"

type cacheSettings struct {
	Size int `flag:"size" usage:"Cache size"`
}

type databaseSettings struct {
	Host string `flag:"host" usage:"Database host"`
	Port int    `flag:"port" usage:"Database port"`
}

type settings struct {
	Port    int              `flag:"port,p"    usage:"Server port"`
	Verbose bool             `flag:"verbose,v" usage:"Verbose logging"`
	Timeout strata.Duration  `flag:"timeout"   usage:"Request timeout"`
	Tags    []string         `flag:"tags"      usage:"Deployment tags"`
	APIKey  string           `flag:"api-key"   strata:"api_key,secret"`
	DB      databaseSettings `flag:"db"`
	Cache   *cacheSettings   `flag:"cache"`

	Internal string `strata:"internal_state"`
	Secret   string `strata:"secret_field,secret"`
}

func run[T any](t *testing.T, initial T, args ...string) (T, error) {
	t.Helper()

	cfg := initial

	cmd := &cobra.Command{
		Use: commandName,
		PersistentPreRunE: func(c *cobra.Command, _ []string) error {
			cfg = initial

			return stratacobra.SyncFlagsToStruct(c, &cfg)
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	}

	if err := stratacobra.RegisterFlags(cmd, &cfg); err != nil {
		return cfg, fmt.Errorf("register flags: %w", err)
	}

	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		return cfg, fmt.Errorf("execute: %w", err)
	}

	return cfg, nil
}

func TestCLIValueSurvivesLoaderReassignment(t *testing.T) {
	t.Parallel()

	got, err := run(t,
		settings{Port: 9000, Verbose: true, Tags: []string{"from-file"}},
		"--port", "1234",
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got.Port != 1234 {
		t.Fatalf("Port = %d, want 1234 (the CLI value was destroyed by loading)", got.Port)
	}

	if !got.Verbose {
		t.Errorf("Verbose = false, want true (the loaded value must survive)")
	}

	if !reflect.DeepEqual(got.Tags, []string{"from-file"}) {
		t.Errorf("Tags = %v, want [from-file]", got.Tags)
	}
}

func TestUnsuppliedFlagsDoNotOverwriteLoadedValues(t *testing.T) {
	t.Parallel()

	registered := settings{Port: 8080, Tags: []string{"from-default"}}
	loaded := settings{Port: 9000, Tags: []string{"from-file"}}

	cfg := registered

	cmd := &cobra.Command{
		Use: commandName,
		PersistentPreRunE: func(c *cobra.Command, _ []string) error {
			cfg = loaded

			return stratacobra.SyncFlagsToStruct(c, &cfg)
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	}

	if err := stratacobra.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register flags: %v", err)
	}

	cmd.SetArgs(nil)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if cfg.Port != 9000 {
		t.Fatalf("Port = %d, want 9000: a flag the user did not supply must not overwrite the loaded value", cfg.Port)
	}

	if !reflect.DeepEqual(cfg.Tags, []string{"from-file"}) {
		t.Fatalf("Tags = %v, want [from-file]", cfg.Tags)
	}
}

func TestPrecedenceTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		loaded int
		args   []string
		want   int
	}{
		{name: "default only", loaded: 8080, args: nil, want: 8080},
		{name: "file overrides default", loaded: 9000, args: nil, want: 9000},
		{name: "cli overrides file", loaded: 9000, args: []string{"--port", "1234"}, want: 1234},
		{name: "cli equal to default still wins", loaded: 10000, args: []string{"--port", "8080"}, want: 8080},
		{name: "explicit zero wins", loaded: 10000, args: []string{"--port", "0"}, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := run(t, settings{Port: tc.loaded}, tc.args...)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			if got.Port != tc.want {
				t.Fatalf("Port = %d, want %d", got.Port, tc.want)
			}
		})
	}
}

func TestUntaggedFieldsAreNotFlags(t *testing.T) {
	t.Parallel()

	cfg := settings{}

	cmd := &cobra.Command{Use: commandName}
	if err := stratacobra.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	for _, absent := range []string{"internal", "internal-state", "internal_state", "secret-field", "secret_field"} {
		if cmd.Flags().Lookup(absent) != nil {
			t.Errorf("--%s must not be registered: the field carries no flag tag", absent)
		}
	}

	for _, present := range []string{"port", "verbose", "timeout", "tags", "api-key", "db-host", "db-port", "cache-size"} {
		if cmd.Flags().Lookup(present) == nil {
			t.Errorf("--%s must be registered", present)
		}
	}
}

func TestEmbeddedStructIsInlined(t *testing.T) {
	t.Parallel()

	type common struct {
		Quiet bool `flag:"quiet,q"`
	}

	type outer struct {
		common

		Port int `flag:"port"`
	}

	cfg := outer{}

	cmd := &cobra.Command{Use: commandName}
	if err := stratacobra.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	if cmd.Flags().Lookup("quiet") == nil {
		t.Errorf("an embedded field must register at the parent namespace")
	}

	if cmd.Flags().Lookup("common-quiet") != nil {
		t.Errorf("an embedded field must not be prefixed with its type name")
	}
}

func TestDerivedNames(t *testing.T) {
	t.Parallel()

	type derived struct {
		MaxConnections int `flag:""`
		HTTPServer     int `flag:",p"`
	}

	cfg := derived{}

	cmd := &cobra.Command{Use: commandName}
	if err := stratacobra.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	if cmd.Flags().Lookup("max-connections") == nil {
		t.Errorf("MaxConnections must derive --max-connections")
	}

	if cmd.Flags().Lookup("http-server") == nil {
		t.Errorf("HTTPServer must derive --http-server")
	}
}

func TestTagGrammarRejections(t *testing.T) {
	t.Parallel()

	t.Run("dash is rejected", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Port int `flag:"-"`
		}

		cfg := target{}
		assertError(t, &cfg, stratacobra.ErrInvalidTag)
	})

	t.Run("multiple shorthands are rejected", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Port int `flag:"port,p,q"`
		}

		cfg := target{}
		assertError(t, &cfg, stratacobra.ErrInvalidTag)
	})

	t.Run("long shorthand is rejected", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Port int `flag:"port,port"`
		}

		cfg := target{}
		assertError(t, &cfg, stratacobra.ErrInvalidTag)
	})

	t.Run("symbolic shorthand is rejected", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Port int `flag:"port,@"`
		}

		cfg := target{}
		assertError(t, &cfg, stratacobra.ErrInvalidTag)
	})

	t.Run("container shorthand is rejected", func(t *testing.T) {
		t.Parallel()

		type child struct {
			Port int `flag:"port"`
		}

		type target struct {
			DB child `flag:"db,d"`
		}

		cfg := target{}
		assertError(t, &cfg, stratacobra.ErrInvalidTag)
	})
}

func assertError(t *testing.T, cfg any, want error) {
	t.Helper()

	cmd := &cobra.Command{Use: commandName}

	err := stratacobra.RegisterFlags(cmd, cfg)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestRegistrationErrors(t *testing.T) {
	t.Parallel()

	t.Run("nil configuration", func(t *testing.T) {
		t.Parallel()

		cmd := &cobra.Command{Use: commandName}
		if err := stratacobra.RegisterFlags(cmd, nil); !errors.Is(err, stratacobra.ErrNotStruct) {
			t.Fatalf("err = %v, want ErrNotStruct", err)
		}
	})

	t.Run("non-pointer configuration", func(t *testing.T) {
		t.Parallel()

		cfg := settings{}
		assertError(t, cfg, stratacobra.ErrNotStruct)
	})

	t.Run("duplicate names", func(t *testing.T) {
		t.Parallel()

		type target struct {
			First  int `flag:"port"`
			Second int `flag:"port"`
		}

		cfg := target{}
		assertError(t, &cfg, stratacobra.ErrDuplicateFlag)
	})

	t.Run("duplicate shorthands", func(t *testing.T) {
		t.Parallel()

		type target struct {
			First  int `flag:"first,p"`
			Second int `flag:"second,p"`
		}

		cfg := target{}
		assertError(t, &cfg, stratacobra.ErrDuplicateFlag)
	})

	t.Run("conflict with a manual flag is transactional", func(t *testing.T) {
		t.Parallel()

		cfg := settings{}

		cmd := &cobra.Command{Use: commandName}
		cmd.Flags().Int("port", 3000, "hand written")

		err := stratacobra.RegisterFlags(cmd, &cfg)
		if !errors.Is(err, stratacobra.ErrFlagConflict) {
			t.Fatalf("err = %v, want ErrFlagConflict", err)
		}

		if cmd.Flags().Lookup("verbose") != nil {
			t.Errorf("registration must be transactional: --verbose was added despite a conflict")
		}
	})

	t.Run("recursive type", func(t *testing.T) {
		t.Parallel()

		cfg := recursive{}
		assertError(t, &cfg, stratacobra.ErrRecursiveType)
	})

	t.Run("unsupported tagged type", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Mapping map[string]string `flag:"mapping"`
		}

		cfg := target{}
		assertError(t, &cfg, stratacobra.ErrUnsupportedFieldType)
	})
}

type recursive struct {
	Next *recursive `flag:"next"`

	Value int `flag:"value"`
}

type wide struct {
	Text      string          `flag:"text"`
	Enabled   bool            `flag:"enabled"`
	Small     int8            `flag:"small"`
	Wide      int64           `flag:"wide"`
	Unsigned  uint32          `flag:"unsigned"`
	Ratio     float64         `flag:"ratio"`
	Delay     time.Duration   `flag:"delay"`
	Times     strata.Duration `flag:"times"`
	Names     []string        `flag:"names"`
	Counts    []int           `flag:"counts"`
	Positions []int64         `flag:"positions"`
}

func TestTypeCoverage(t *testing.T) {
	t.Parallel()

	got, err := run(t, wide{},
		"--text", "hello",
		"--enabled",
		"--small", "7",
		"--wide", "9000000000",
		"--unsigned", "42",
		"--ratio", "1.5",
		"--delay", "250ms",
		"--times", "7d",
		"--names", "alpha,beta",
		"--counts", "1,2,3",
		"--positions", "10,20",
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got.Text != "hello" || !got.Enabled || got.Small != 7 || got.Wide != 9000000000 {
		t.Fatalf("scalar decode mismatch: %+v", got)
	}

	if got.Unsigned != 42 || got.Ratio != 1.5 {
		t.Fatalf("numeric decode mismatch: %+v", got)
	}

	if got.Delay != 250*time.Millisecond {
		t.Errorf("Delay = %s, want 250ms", got.Delay)
	}

	if got.Times.Duration() != 7*24*time.Hour {
		t.Errorf("Times = %s, want 168h (7d)", got.Times)
	}

	if !reflect.DeepEqual(got.Names, []string{"alpha", "beta"}) {
		t.Errorf("Names = %v", got.Names)
	}

	if !reflect.DeepEqual(got.Counts, []int{1, 2, 3}) {
		t.Errorf("Counts = %v", got.Counts)
	}

	if !reflect.DeepEqual(got.Positions, []int64{10, 20}) {
		t.Errorf("Positions = %v", got.Positions)
	}
}

func TestNarrowIntegerOverflowIsReported(t *testing.T) {
	t.Parallel()

	type narrow struct {
		Small int8 `flag:"small"`
	}

	_, err := run(t, narrow{}, "--small", "300")
	if err == nil {
		t.Fatalf("expected an overflow error")
	}

	if !errors.Is(err, stratacobra.ErrValueOverflow) {
		t.Fatalf("err = %v, want ErrValueOverflow", err)
	}
}

func TestSliceValuesRoundTrip(t *testing.T) {
	t.Parallel()

	got, err := run(t, settings{}, "--tags", `a,b,""`+`,"quote"`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(got.Tags) == 0 || got.Tags[0] != "a" {
		t.Fatalf("Tags = %q, want a first", got.Tags)
	}
}

func TestSliceReplacementDoesNotAppend(t *testing.T) {
	t.Parallel()

	got, err := run(t, settings{Tags: []string{"from-file"}}, "--tags", "from-cli")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !reflect.DeepEqual(got.Tags, []string{"from-cli"}) {
		t.Fatalf("Tags = %v, want [from-cli]", got.Tags)
	}
}

func TestPointerAllocation(t *testing.T) {
	t.Parallel()

	t.Run("absent subtree stays nil", func(t *testing.T) {
		t.Parallel()

		got, err := run(t, settings{})
		if err != nil {
			t.Fatalf("run: %v", err)
		}

		if got.Cache != nil {
			t.Fatalf("Cache = %+v, want nil", got.Cache)
		}
	})

	t.Run("explicit child allocates the subtree", func(t *testing.T) {
		t.Parallel()

		got, err := run(t, settings{}, "--cache-size", "128")
		if err != nil {
			t.Fatalf("run: %v", err)
		}

		if got.Cache == nil || got.Cache.Size != 128 {
			t.Fatalf("Cache = %+v, want size 128", got.Cache)
		}
	})
}

func TestSecretDefaultsAreNotPublished(t *testing.T) {
	t.Parallel()

	cfg := settings{APIKey: "leaked-default"}

	cmd := &cobra.Command{Use: commandName}
	if err := stratacobra.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	flag := cmd.Flags().Lookup("api-key")
	if flag == nil {
		t.Fatalf("--api-key must be registered")
	}

	if flag.DefValue != "" {
		t.Fatalf("DefValue = %q, want empty: a secret default must not be published", flag.DefValue)
	}
}

func TestSecretOriginsAreRedacted(t *testing.T) {
	t.Parallel()

	meta := strata.NewMetadata()
	cfg := settings{}

	cmd := &cobra.Command{
		Use: commandName,
		PreRunE: func(c *cobra.Command, _ []string) error {
			return stratacobra.SyncFlagsToStruct(c, &cfg, stratacobra.WithMetadata(meta))
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	}

	if err := stratacobra.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	cmd.SetArgs([]string{"--api-key", "super-secret", "--port", "1234"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	secretOrigin, ok := meta.Where("api_key")
	if !ok {
		t.Fatalf("api_key origin missing")
	}

	if secretOrigin.RawValue != "[REDACTED]" {
		t.Errorf("RawValue = %q, want [REDACTED]", secretOrigin.RawValue)
	}

	if secretOrigin.Source != strata.SourceFlag || secretOrigin.Path != "--api-key" {
		t.Errorf("origin = %+v, want SourceFlag via --api-key", secretOrigin)
	}

	portOrigin, ok := meta.Where("port")
	if !ok {
		t.Fatalf("port origin missing")
	}

	if portOrigin.RawValue != "1234" {
		t.Errorf("port RawValue = %q, want 1234", portOrigin.RawValue)
	}
}

func TestManualFlagsCoexist(t *testing.T) {
	t.Parallel()

	cfg := settings{}

	cmd := &cobra.Command{
		Use: commandName,
		PreRunE: func(c *cobra.Command, _ []string) error {
			cfg = settings{Port: 9000}

			return stratacobra.SyncFlagsToStruct(c, &cfg)
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	}

	if err := stratacobra.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	cmd.Flags().String("log-format", "text", "hand written")
	cmd.SetArgs([]string{"--log-format", "json"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	format, err := cmd.Flags().GetString("log-format")
	if err != nil {
		t.Fatalf("read log-format: %v", err)
	}

	if format != "json" {
		t.Errorf("log-format = %q, want json", format)
	}

	if len(cfg.Tags) != 0 {
		t.Errorf("Tags = %v, want untouched", cfg.Tags)
	}
}

func TestNestedPrefixes(t *testing.T) {
	t.Parallel()

	got, err := run(t, settings{}, "--db-host", "db.internal", "--db-port", "5432")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got.DB.Host != "db.internal" || got.DB.Port != 5432 {
		t.Fatalf("DB = %+v, want db.internal:5432", got.DB)
	}
}

func TestPersistentRegistration(t *testing.T) {
	t.Parallel()

	cfg := settings{}

	cmd := &cobra.Command{Use: commandName}
	if err := stratacobra.RegisterFlags(cmd, &cfg, stratacobra.WithPersistent()); err != nil {
		t.Fatalf("register: %v", err)
	}

	if cmd.PersistentFlags().Lookup("port") == nil {
		t.Fatalf("--port must be persistent")
	}

	if cmd.Flags().Lookup("port") != nil {
		t.Fatalf("--port must not also be local")
	}
}

type validatedSettings struct {
	Port int `flag:"port" yaml:"port"`
}

func (s *validatedSettings) Validate() error {
	if s.Port < 1024 {
		return fmt.Errorf("port must be >= 1024: got %d", s.Port)
	}

	return nil
}

func TestWithFlagsParticipatesInCascadeAndRecordsProvenance(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: commandName}

	var seed settings

	if err := stratacobra.RegisterFlags(cmd, &seed); err != nil {
		t.Fatalf("register: %v", err)
	}

	cmd.SetArgs([]string{"--port", "9090"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	var (
		loaded settings
		meta   *strata.Metadata
	)

	cmd.RunE = func(c *cobra.Command, _ []string) error {
		var err error

		loaded, meta, err = strata.LoadWithMetadata[settings](
			strata.WithContribution(func(target any, _ *strata.Metadata) error {
				s, ok := target.(*settings)
				if !ok {
					return errors.New("target is not *settings")
				}

				s.Port = 8080
				s.Verbose = true

				return nil
			}),
			stratacobra.WithFlags(c, stratacobra.WithMetadata(nil)),
		)

		return err
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if loaded.Port != 9090 {
		t.Errorf("Port = %d, want 9090 (CLI flag should override lower layer)", loaded.Port)
	}

	if !loaded.Verbose {
		t.Errorf("Verbose = false, want true (unmentioned flag retains layer value)")
	}

	origin, ok := meta.Where("port")
	if !ok {
		t.Fatal("Where(port) not recorded")
	}

	if origin.Source != strata.SourceFlag || origin.RawValue != "9090" {
		t.Errorf("origin = %+v, want SourceFlag and raw 9090", origin)
	}
}

func TestWithFlagsCanSatisfyValidation(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: commandName}

	var seed validatedSettings

	if err := stratacobra.RegisterFlags(cmd, &seed); err != nil {
		t.Fatalf("register: %v", err)
	}

	cmd.SetArgs([]string{"--port", "8080"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	var loaded validatedSettings

	cmd.RunE = func(c *cobra.Command, _ []string) error {
		var err error

		loaded, err = strata.Load[validatedSettings](
			strata.WithContribution(func(target any, _ *strata.Metadata) error {
				v, ok := target.(*validatedSettings)
				if !ok {
					return errors.New("target is not *validatedSettings")
				}

				v.Port = 80

				return nil
			}),
			stratacobra.WithFlags(c),
		)

		return err
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v, expected validation to succeed after CLI override", err)
	}

	if loaded.Port != 8080 {
		t.Errorf("Port = %d, want 8080", loaded.Port)
	}
}

func TestWithFlagsCanViolateValidation(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: commandName}

	var seed validatedSettings

	if err := stratacobra.RegisterFlags(cmd, &seed); err != nil {
		t.Fatalf("register: %v", err)
	}

	cmd.SetArgs([]string{"--port", "80"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	cmd.RunE = func(c *cobra.Command, _ []string) error {
		_, err := strata.Load[validatedSettings](
			strata.WithContribution(func(target any, _ *strata.Metadata) error {
				v, ok := target.(*validatedSettings)
				if !ok {
					return errors.New("target is not *validatedSettings")
				}

				v.Port = 8080

				return nil
			}),
			stratacobra.WithFlags(c),
		)

		return err
	}

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected validation error from invalid CLI flag, got nil")
	}
}
