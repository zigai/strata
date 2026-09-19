package stratacobra_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/spf13/cobra"

	"github.com/zigai/strata"
	stratacobra "github.com/zigai/strata/bridge/cobra"
)

type cliConfig struct {
	Sort string `flag:"sort"`
	Port int    `flag:"port"`
}

func TestCobraBridgeParserDefaultTrap(t *testing.T) {
	t.Parallel()

	t.Run("omitted flag is populated from config", func(t *testing.T) {
		t.Parallel()

		cfg := cliConfig{
			Sort: "created_at",
			Port: 8080,
		}

		var (
			recordedSort string
			recordedPort int
		)

		cmd := &cobra.Command{
			Use: commandName,
			RunE: func(c *cobra.Command, args []string) error {
				if err := stratacobra.Apply(c, cfg); err != nil {
					return fmt.Errorf("apply configuration: %w", err)
				}

				s, err := c.Flags().GetString("sort")
				if err != nil {
					return fmt.Errorf("read sort: %w", err)
				}

				p, err := c.Flags().GetInt("port")
				if err != nil {
					return fmt.Errorf("read port: %w", err)
				}

				recordedSort = s
				recordedPort = p

				return nil
			},
		}

		cmd.Flags().String("sort", "default_parser_value", "Sort order")
		cmd.Flags().Int("port", 3000, "Port number")

		cmd.SetArgs([]string{})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("cmd.Execute error: %v", err)
		}

		if recordedSort != "created_at" {
			t.Errorf("sort = %q, want created_at (failed to defeat parser default trap!)", recordedSort)
		}

		if recordedPort != 8080 {
			t.Errorf("port = %d, want 8080", recordedPort)
		}
	})

	t.Run("explicitly set flag on CLI is retained", func(t *testing.T) {
		t.Parallel()

		cfg := cliConfig{
			Sort: "created_at",
			Port: 8080,
		}

		var (
			recordedSort string
			recordedPort int
		)

		cmd := &cobra.Command{
			Use: commandName,
			RunE: func(c *cobra.Command, args []string) error {
				if err := stratacobra.Apply(c, cfg); err != nil {
					return fmt.Errorf("apply configuration: %w", err)
				}

				s, err := c.Flags().GetString("sort")
				if err != nil {
					return fmt.Errorf("read sort: %w", err)
				}

				p, err := c.Flags().GetInt("port")
				if err != nil {
					return fmt.Errorf("read port: %w", err)
				}

				recordedSort = s
				recordedPort = p

				return nil
			},
		}

		cmd.Flags().String("sort", "default_parser_value", "Sort order")
		cmd.Flags().Int("port", 3000, "Port number")

		cmd.SetArgs([]string{"--sort", "user_cli_value"})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("cmd.Execute error: %v", err)
		}

		if recordedSort != "user_cli_value" {
			t.Errorf("sort = %q, want user_cli_value (user flag should not be overwritten)", recordedSort)
		}

		// port was omitted on the CLI and receives the configuration value.
		if recordedPort != 8080 {
			t.Errorf("port = %d, want 8080", recordedPort)
		}
	})
}

type sliceAndShortConfig struct {
	Items []string `flag:"items"`
	P     int      `flag:"p"`
}

func TestCobraBridgeSliceAndShorthand(t *testing.T) {
	t.Parallel()

	cfg := sliceAndShortConfig{
		Items: []string{"alpha", "beta", "gamma"},
		P:     9090,
	}

	cmd := &cobra.Command{
		Use: commandName,
	}

	cmd.Flags().StringSlice("items", []string{"default"}, "List of items")
	cmd.Flags().IntP("port", "p", 3000, "Port")

	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := stratacobra.Apply(cmd, cfg); err != nil {
		t.Fatalf("stratacobra.Apply error: %v", err)
	}

	items, err := cmd.Flags().GetStringSlice("items")
	if err != nil {
		t.Fatalf("GetStringSlice error: %v", err)
	}

	if len(items) != 3 || items[0] != "alpha" || items[1] != "beta" || items[2] != "gamma" {
		t.Errorf("items = %v, want [alpha beta gamma]", items)
	}

	port, err := cmd.Flags().GetInt("port")
	if err != nil {
		t.Fatalf("GetInt error: %v", err)
	}

	if port != 9090 {
		t.Errorf("port = %d, want 9090 (shorthand lookup failed)", port)
	}
}

type pointerAndCommaConfig struct {
	Port    *int     `flag:"port"`
	Missing *string  `flag:"missing"`
	Tags    []string `flag:"tags"`
}

func TestCobraBridgePointersAndCommas(t *testing.T) {
	t.Parallel()

	portVal := 7070
	cfg := pointerAndCommaConfig{
		Port:    &portVal,
		Missing: nil,
		Tags:    []string{"key=val,a=b", "simple"},
	}

	cmd := &cobra.Command{Use: commandName}
	cmd.Flags().Int("port", 3000, "Port")
	cmd.Flags().String("missing", "default_str", "Missing string")
	cmd.Flags().StringSlice("tags", []string{}, "Tags")

	if err := stratacobra.Apply(cmd, cfg); err != nil {
		t.Fatalf("Apply error: %v", err)
	}

	gotPort, err := cmd.Flags().GetInt("port")
	if err != nil {
		t.Fatalf("GetInt error: %v", err)
	}

	if gotPort != 7070 {
		t.Errorf("gotPort = %d, want 7070 (pointer dereference failed)", gotPort)
	}

	// The nil pointer must not overwrite the registered default value.
	gotMissing, err := cmd.Flags().GetString("missing")
	if err != nil {
		t.Fatalf("GetString error: %v", err)
	}

	if gotMissing != "default_str" {
		t.Errorf("gotMissing = %q, want default_str", gotMissing)
	}
}

type embeddedInner struct {
	Port int `flag:"port"`
}

type embeddedOuter struct {
	embeddedInner
}

func TestEmbeddedStructConfigKeyProvenance(t *testing.T) {
	t.Parallel()

	cfg := &embeddedOuter{}

	cmd := &cobra.Command{Use: "app"}
	if err := stratacobra.RegisterFlags(cmd, cfg); err != nil {
		t.Fatalf("RegisterFlags error: %v", err)
	}

	cmd.SetArgs([]string{"--port", "9090"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("cmd.Execute error: %v", err)
	}

	meta := strata.NewMetadata()
	if err := stratacobra.SyncFlagsToStruct(cmd, cfg, stratacobra.WithMetadata(meta)); err != nil {
		t.Fatalf("SyncFlagsToStruct error: %v", err)
	}

	origin, ok := meta.Where("port")
	if !ok {
		t.Fatalf("Where('port') was not found in metadata; embedded fields must be inlined in config key")
	}

	if origin.Source != strata.SourceFlag || origin.RawValue != "9090" {
		t.Errorf("origin = %+v, want Source=flag, RawValue=9090", origin)
	}
}

type namedSub struct {
	Host string `flag:"host"`
}

type namedContainer struct {
	Sub namedSub `flag:""`
}

func TestNamedStructContainerEmptyFlagPrefix(t *testing.T) {
	t.Parallel()

	cfg := &namedContainer{}

	cmd := &cobra.Command{Use: "app"}
	if err := stratacobra.RegisterFlags(cmd, cfg); err != nil {
		t.Fatalf("RegisterFlags error: %v", err)
	}

	if cmd.Flags().Lookup("sub-host") == nil {
		t.Errorf("expected flag --sub-host to be registered for flag:\"\" container")
	}
}

type (
	myInt    int
	myString string
)

type customSliceConfig struct {
	Ints    []myInt    `flag:"ints"`
	Strings []myString `flag:"strings"`
}

func TestCustomSliceTypesCobra(t *testing.T) {
	t.Parallel()

	cfg := &customSliceConfig{
		Ints:    []myInt{1, 2},
		Strings: []myString{"a", "b"},
	}

	cmd := &cobra.Command{Use: "app"}
	if err := stratacobra.RegisterFlags(cmd, cfg); err != nil {
		t.Fatalf("RegisterFlags with custom slice defaults failed: %v", err)
	}

	cmd.SetArgs([]string{"--ints", "10,20", "--strings", "x,y"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("cmd.Execute error: %v", err)
	}

	if err := stratacobra.SyncFlagsToStruct(cmd, cfg); err != nil {
		t.Fatalf("SyncFlagsToStruct failed for custom slice types: %v", err)
	}

	if len(cfg.Ints) != 2 || cfg.Ints[0] != 10 || cfg.Ints[1] != 20 {
		t.Errorf("cfg.Ints = %v, want [10 20]", cfg.Ints)
	}

	if len(cfg.Strings) != 2 || cfg.Strings[0] != "x" || cfg.Strings[1] != "y" {
		t.Errorf("cfg.Strings = %v, want [x y]", cfg.Strings)
	}
}

type persistentConflictConfig struct {
	Port int `flag:"port"`
}

func TestPersistentFlagConflict(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "app"}
	cmd.PersistentFlags().Int("port", 1234, "persistent port")

	cfg := &persistentConflictConfig{}

	err := stratacobra.RegisterFlags(cmd, cfg)
	if err == nil {
		t.Fatalf("expected ErrFlagConflict when registering local flag that shadows persistent flag, got nil")
	}

	if !errors.Is(err, stratacobra.ErrFlagConflict) {
		t.Errorf("expected ErrFlagConflict, got %v", err)
	}
}
