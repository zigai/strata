package strataurfave_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/zigai/strata"
	strataurfave "github.com/zigai/strata/bridge/urfave"
)

// commandName is shared by the test command fixtures.
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

	// No flag tag: configuration only.
	Internal string `strata:"internal_state"`
	Secret   string `strata:"secret_field,secret"`
}

// command builds a test command whose before hook is before.
func command(before cli.BeforeFunc) *cli.Command {
	return &cli.Command{
		Name:      commandName,
		Writer:    io.Discard,
		ErrWriter: io.Discard,
		Before:    before,
		Action:    func(context.Context, *cli.Command) error { return nil },
	}
}

// run registers flags from initial, executes the command with args, and returns
// the configuration the application would observe. The before hook replaces the
// caller's value wholesale, as strata.Load does, and then runs the canonical
// Sync-then-Apply order.
func run[T any](t *testing.T, initial T, args ...string) (T, error) {
	t.Helper()

	cfg := initial

	cmd := command(func(ctx context.Context, c *cli.Command) (context.Context, error) {
		// The loader hands back a freshly decoded value. The parsed CLI value
		// survives because generated flags own detached storage.
		cfg = initial

		if err := strataurfave.SyncFlagsToStruct(c, &cfg); err != nil {
			return ctx, fmt.Errorf("sync: %w", err)
		}

		return ctx, strataurfave.Apply(c, cfg)
	})

	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		return cfg, fmt.Errorf("register flags: %w", err)
	}

	if err := cmd.Run(context.Background(), append([]string{commandName}, args...)); err != nil {
		return cfg, fmt.Errorf("execute: %w", err)
	}

	return cfg, nil
}

// flagNamed reports the registered flag that answers to name, including aliases.
func flagNamed(cmd *cli.Command, name string) cli.Flag {
	for _, flag := range cmd.Flags {
		if slices.Contains(flag.Names(), name) {
			return flag
		}
	}

	return nil
}

// --- detached storage -------------------------------------------------------

func TestCLIValueSurvivesLoaderReassignment(t *testing.T) {
	t.Parallel()

	// The loader supplies a different value for Port. Generated flags own
	// detached storage, and the parsed CLI value still wins.
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

	// Registration seeded --port with 8080. The loader then supplies 9000 for a
	// field the user never mentioned, and synchronization MUST leave it alone:
	// writing the registered value back would destroy the loaded tier.
	cfg := settings{Port: 8080}

	cmd := command(func(ctx context.Context, c *cli.Command) (context.Context, error) {
		cfg = settings{Port: 9000}

		if err := strataurfave.SyncFlagsToStruct(c, &cfg); err != nil {
			return ctx, fmt.Errorf("sync: %w", err)
		}

		return ctx, nil
	})

	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := cmd.Run(context.Background(), []string{commandName}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if cfg.Port != 9000 {
		t.Fatalf("Port = %d, want 9000: a flag the user did not supply must not be written back", cfg.Port)
	}
}

func TestStorageIsDetachedFromTheCaller(t *testing.T) {
	t.Parallel()

	tags := []string{"from-file"}
	cfg := settings{Port: 8080, Tags: tags}

	cmd := command(nil)

	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	// The loader replaces the caller's value, and the original slice backing
	// array is mutated afterwards. Neither change may reach the registered flags.
	cfg = settings{Port: 9999, Tags: []string{"replaced"}}
	tags[0] = "mutated"

	if err := cmd.Run(context.Background(), []string{commandName}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if value := cmd.Int("port"); value != 8080 {
		t.Errorf("port = %d, want 8080: the flag must keep the value it was seeded with", value)
	}

	if value := cmd.StringSlice("tags"); !reflect.DeepEqual(value, []string{"from-file"}) {
		t.Errorf("tags = %v, want [from-file]: slice storage must own a clone", value)
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

func TestShorthandSuppliesTheSameFlag(t *testing.T) {
	t.Parallel()

	// urfave/cli has no separate shorthand concept: the tag's shorthand becomes
	// an alias, and supplying it MUST mark the long name as supplied as well.
	got, err := run(t, settings{Port: 9000}, "-p", "1234")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got.Port != 1234 {
		t.Fatalf("Port = %d, want 1234 (the shorthand must mark --port as supplied)", got.Port)
	}
}

// --- discovery --------------------------------------------------------------

func TestUntaggedFieldsAreNotFlags(t *testing.T) {
	t.Parallel()

	cfg := settings{}

	cmd := command(nil)
	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	for _, absent := range []string{"internal", "internal-state", "internal_state", "secret-field", "secret_field"} {
		if flagNamed(cmd, absent) != nil {
			t.Errorf("--%s must not be registered: the field carries no flag tag", absent)
		}
	}

	for _, present := range []string{"port", "verbose", "timeout", "tags", "api-key", "db-host", "db-port", "cache-size"} {
		if flagNamed(cmd, present) == nil {
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

	cmd := command(nil)
	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	if flagNamed(cmd, "quiet") == nil {
		t.Errorf("an embedded field must register at the parent namespace")
	}

	if flagNamed(cmd, "common-quiet") != nil {
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

	cmd := command(nil)
	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	if flagNamed(cmd, "max-connections") == nil {
		t.Errorf("MaxConnections must derive --max-connections")
	}

	if flagNamed(cmd, "http-server") == nil {
		t.Errorf("HTTPServer must derive --http-server")
	}

	if flagNamed(cmd, "p") == nil {
		t.Errorf("HTTPServer must carry shorthand -p")
	}
}

// --- tag grammar ------------------------------------------------------------

func TestTagGrammarRejections(t *testing.T) {
	t.Parallel()

	t.Run("dash is rejected", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Port int `flag:"-"`
		}

		cfg := target{}
		assertError(t, &cfg, strataurfave.ErrInvalidTag)
	})

	t.Run("multiple shorthands are rejected", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Port int `flag:"port,p,q"`
		}

		cfg := target{}
		assertError(t, &cfg, strataurfave.ErrInvalidTag)
	})

	t.Run("long shorthand is rejected", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Port int `flag:"port,port"`
		}

		cfg := target{}
		assertError(t, &cfg, strataurfave.ErrInvalidTag)
	})

	t.Run("symbolic shorthand is rejected", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Port int `flag:"port,@"`
		}

		cfg := target{}
		assertError(t, &cfg, strataurfave.ErrInvalidTag)
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
		assertError(t, &cfg, strataurfave.ErrInvalidTag)
	})
}

func assertError(t *testing.T, cfg any, want error) {
	t.Helper()

	cmd := command(nil)

	err := strataurfave.RegisterFlags(cmd, cfg)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}

	if len(cmd.Flags) != 0 {
		t.Fatalf("registration must be transactional: %d flags were added", len(cmd.Flags))
	}
}

// --- registration errors ----------------------------------------------------

func TestRegistrationErrors(t *testing.T) {
	t.Parallel()

	t.Run("nil configuration", func(t *testing.T) {
		t.Parallel()

		cmd := command(nil)
		if err := strataurfave.RegisterFlags(cmd, nil); !errors.Is(err, strataurfave.ErrNotStruct) {
			t.Fatalf("err = %v, want ErrNotStruct", err)
		}
	})

	t.Run("non-pointer configuration", func(t *testing.T) {
		t.Parallel()

		cfg := settings{}
		assertError(t, cfg, strataurfave.ErrNotStruct)
	})

	t.Run("duplicate names", func(t *testing.T) {
		t.Parallel()

		type target struct {
			First  int `flag:"port"`
			Second int `flag:"port"`
		}

		cfg := target{}
		assertError(t, &cfg, strataurfave.ErrDuplicateFlag)
	})

	t.Run("duplicate shorthands", func(t *testing.T) {
		t.Parallel()

		type target struct {
			First  int `flag:"first,p"`
			Second int `flag:"second,p"`
		}

		cfg := target{}
		assertError(t, &cfg, strataurfave.ErrDuplicateFlag)
	})

	t.Run("recursive type", func(t *testing.T) {
		t.Parallel()

		cfg := recursive{}
		assertError(t, &cfg, strataurfave.ErrRecursiveType)
	})

	t.Run("unsupported tagged type", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Mapping map[string]string `flag:"mapping"`
		}

		cfg := target{}
		assertError(t, &cfg, strataurfave.ErrUnsupportedFieldType)
	})

	t.Run("untagged unsupported type is skipped", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Mapping map[string]string

			Port int `flag:"port"`
		}

		cfg := target{}

		cmd := command(nil)
		if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
			t.Fatalf("register: %v", err)
		}

		if flagNamed(cmd, "port") == nil {
			t.Errorf("--port must be registered")
		}
	})
}

type recursive struct {
	Next *recursive `flag:"next"`

	Value int `flag:"value"`
}

// --- type coverage ----------------------------------------------------------

type wide struct {
	Text      string          `flag:"text"`
	Enabled   bool            `flag:"enabled"`
	Small     int8            `flag:"small"`
	Wide      int64           `flag:"wide"`
	Unsigned  uint32          `flag:"unsigned"`
	Tiny      uint16          `flag:"tiny"`
	Fraction  float32         `flag:"fraction"`
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
		"--tiny", "9",
		"--fraction", "0.25",
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

	if got.Unsigned != 42 || got.Tiny != 9 {
		t.Fatalf("unsigned decode mismatch: %+v", got)
	}

	if got.Fraction != 0.25 || got.Ratio != 1.5 {
		t.Fatalf("float decode mismatch: %+v", got)
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

	if !errors.Is(err, strataurfave.ErrValueOverflow) {
		t.Fatalf("err = %v, want ErrValueOverflow", err)
	}
}

// --- slices -----------------------------------------------------------------

func TestSliceValuesRoundTrip(t *testing.T) {
	t.Parallel()

	got, err := run(t, settings{}, "--tags", "a,b")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !reflect.DeepEqual(got.Tags, []string{"a", "b"}) {
		t.Fatalf("Tags = %v, want [a b]", got.Tags)
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

func TestForeignSliceFlagIsReplacedOnTheFirstWrite(t *testing.T) {
	t.Parallel()

	// A flag the bridge did not generate exposes no destination it can address,
	// and Apply falls back to urfave/cli's string interface. urfave/cli clears a
	// slice on its first write, which replaces the registered default.
	type tagged struct {
		Items []string `flag:"items"`
	}

	cfg := tagged{Items: []string{"first", "second"}}

	cmd := command(nil)
	cmd.Flags = append(cmd.Flags, &cli.StringSliceFlag{Name: "items", Value: []string{"default"}})

	if err := strataurfave.Apply(cmd, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := cmd.StringSlice("items"); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("items = %v, want [first second]", got)
	}
}

func TestSliceElementsSurviveApply(t *testing.T) {
	t.Parallel()

	// Elements containing commas, quotes, and spaces do not survive a
	// comma-joined round trip. The configuration-to-flag direction MUST assign
	// the slice and not serialize it.
	cfg := settings{Tags: []string{"a,b", `"quoted"`, "", "back\\slash"}}

	var observed []string

	cmd := command(func(ctx context.Context, c *cli.Command) (context.Context, error) {
		if err := strataurfave.Apply(c, cfg); err != nil {
			return ctx, fmt.Errorf("apply: %w", err)
		}

		observed = c.StringSlice("tags")

		return ctx, nil
	})

	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := cmd.Run(context.Background(), []string{commandName}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !reflect.DeepEqual(observed, cfg.Tags) {
		t.Fatalf("observed = %q, want %q", observed, cfg.Tags)
	}
}

// --- pointers ---------------------------------------------------------------

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

// --- secrets ----------------------------------------------------------------

func TestSecretDefaultsAreNotPublished(t *testing.T) {
	t.Parallel()

	cfg := settings{APIKey: "leaked-default"}

	output := &bytes.Buffer{}

	cmd := command(nil)
	cmd.Writer = output

	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	flag := flagNamed(cmd, "api-key")
	if flag == nil {
		t.Fatalf("--api-key must be registered")
	}

	documented, ok := flag.(cli.DocGenerationFlag)
	if !ok {
		t.Fatalf("--api-key must support help generation")
	}

	if documented.IsDefaultVisible() {
		t.Errorf("--api-key must not publish its default")
	}

	if err := cmd.Run(context.Background(), []string{commandName, "--help"}); err != nil {
		t.Fatalf("help: %v", err)
	}

	if strings.Contains(output.String(), "leaked-default") {
		t.Errorf("help output leaked a secret default:\n%s", output.String())
	}

	if !strings.Contains(output.String(), "api-key") {
		t.Errorf("help output must still list --api-key:\n%s", output.String())
	}
}

func TestSecretOriginsAreRedacted(t *testing.T) {
	t.Parallel()

	meta := strata.NewMetadata()
	cfg := settings{}

	cmd := command(func(ctx context.Context, c *cli.Command) (context.Context, error) {
		if err := strataurfave.SyncFlagsToStruct(c, &cfg, strataurfave.WithMetadata(meta)); err != nil {
			return ctx, fmt.Errorf("sync: %w", err)
		}

		return ctx, nil
	})

	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := cmd.Run(context.Background(), []string{commandName, "--api-key", "super-secret", "--port", "1234"}); err != nil {
		t.Fatalf("run: %v", err)
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

	if portOrigin.Source != strata.SourceFlag || portOrigin.Path != "--port" {
		t.Errorf("origin = %+v, want SourceFlag via --port", portOrigin)
	}
}

// --- manual coexistence -----------------------------------------------------

func TestManualFlagsCoexist(t *testing.T) {
	t.Parallel()

	cfg := settings{}

	cmd := command(func(ctx context.Context, c *cli.Command) (context.Context, error) {
		cfg = settings{Port: 9000}

		if err := strataurfave.SyncFlagsToStruct(c, &cfg); err != nil {
			return ctx, fmt.Errorf("sync: %w", err)
		}

		return ctx, strataurfave.Apply(c, cfg)
	})

	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	cmd.Flags = append(cmd.Flags, &cli.StringFlag{Name: "log-format", Value: "text", Usage: "hand written"})

	if err := cmd.Run(context.Background(), []string{commandName, "--log-format", "json"}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if format := cmd.String("log-format"); format != "json" {
		t.Errorf("log-format = %q, want json", format)
	}

	if len(cfg.Tags) != 0 {
		t.Errorf("Tags = %v, want untouched", cfg.Tags)
	}
}

// --- nested containers ------------------------------------------------------

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

// --- generation -------------------------------------------------------------

func TestGeneratedFlagsParseAndSynchronize(t *testing.T) {
	t.Parallel()

	cfg := settings{Port: 9000}

	flags, err := strataurfave.GenerateFlags(&cfg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if len(flags) == 0 {
		t.Fatalf("GenerateFlags must produce flags for tagged fields")
	}

	cmd := command(func(ctx context.Context, c *cli.Command) (context.Context, error) {
		if err := strataurfave.SyncFlagsToStruct(c, &cfg); err != nil {
			return ctx, fmt.Errorf("sync: %w", err)
		}

		return ctx, nil
	})
	cmd.Flags = flags

	if err := cmd.Run(context.Background(), []string{commandName, "--port", "1234"}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if cfg.Port != 1234 {
		t.Fatalf("Port = %d, want 1234", cfg.Port)
	}
}

// --- legacy Apply path ------------------------------------------------------

func TestApplyLeavesExplicitFlagsAlone(t *testing.T) {
	t.Parallel()

	cfg := settings{Port: 9000}

	cmd := command(nil)

	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := cmd.Run(context.Background(), []string{commandName, "--port", "1234"}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if err := strataurfave.Apply(cmd, settings{Port: 9000}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if value := cmd.Int("port"); value != 1234 {
		t.Fatalf("port = %d, want 1234: Apply must not overwrite a supplied flag", value)
	}
}

func TestApplyMarksFlagsAsSupplied(t *testing.T) {
	t.Parallel()

	// Apply writes through Command.Set, which flips urfave/cli's supplied marker.
	// This test pins the asymmetry behind the documented rule: SyncFlagsToStruct
	// runs before Apply.
	cfg := settings{Port: 9000}

	cmd := command(nil)

	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := cmd.Run(context.Background(), []string{commandName}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if cmd.IsSet("port") {
		t.Fatalf("--port must not be marked as supplied: the user passed nothing")
	}

	if err := strataurfave.Apply(cmd, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !cmd.IsSet("port") {
		t.Fatalf("Apply must mark --port as supplied; synchronization depends on running first")
	}
}

func TestApplyToleratesUnusableConfigurations(t *testing.T) {
	t.Parallel()

	cfg := settings{}

	if err := strataurfave.Apply(nil, cfg); err != nil {
		t.Fatalf("apply(nil command): %v", err)
	}

	if err := strataurfave.SyncFlagsToStruct(nil, &cfg); err != nil {
		t.Fatalf("sync(nil command): %v", err)
	}

	var (
		pointer *settings
		scalar  = 7
	)

	for _, unusable := range []any{nil, pointer, scalar} {
		cmd := command(nil)

		if err := strataurfave.RegisterFlags(cmd, &settings{Port: 8080}); err != nil {
			t.Fatalf("register: %v", err)
		}

		if err := strataurfave.Apply(cmd, unusable); err != nil {
			t.Fatalf("apply(%v): %v", unusable, err)
		}

		if value := cmd.Int("port"); value != 8080 {
			t.Errorf("apply(%v) left port = %d, want the registered 8080", unusable, value)
		}
	}
}

func TestUntaggedFieldsMatchHandRegisteredFlags(t *testing.T) {
	t.Parallel()

	// Apply keeps the historical lenient matching: a hand-registered flag derived
	// from an untagged field name still receives the configuration value.
	type legacy struct {
		SortOrder string
	}

	cfg := legacy{SortOrder: "created_at"}

	cmd := command(nil)
	cmd.Flags = append(cmd.Flags, &cli.StringFlag{Name: "sort-order", Value: "text"})

	if err := strataurfave.Apply(cmd, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := cmd.String("sort-order"); got != "created_at" {
		t.Fatalf("sort-order = %q, want created_at", got)
	}
}
