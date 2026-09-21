package strataurfave_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/urfave/cli/v3"

	strataurfave "github.com/zigai/strata/bridge/urfave"
)

type cliConfig struct {
	Sort string `flag:"sort"`
	Port int    `flag:"port"`
}

func TestUrfaveBridgeParserDefaultTrap(t *testing.T) {
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

		cmd := &cli.Command{
			Name: "test",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "sort",
					Value: "default_parser_value", // a parser default the configuration must override
				},
				&cli.IntFlag{
					Name:  "port",
					Value: 3000,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				if err := strataurfave.Apply(c, cfg); err != nil {
					return fmt.Errorf("apply configuration: %w", err)
				}

				recordedSort = c.String("sort")
				recordedPort = c.Int("port")

				return nil
			},
		}

		// No arguments: the user omits every flag.
		if err := cmd.Run(context.Background(), []string{"test"}); err != nil {
			t.Fatalf("cmd.Run error: %v", err)
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

		cmd := &cli.Command{
			Name: "test",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "sort",
					Value: "default_parser_value",
				},
				&cli.IntFlag{
					Name:  "port",
					Value: 3000,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				if err := strataurfave.Apply(c, cfg); err != nil {
					return fmt.Errorf("apply configuration: %w", err)
				}

				recordedSort = c.String("sort")
				recordedPort = c.Int("port")

				return nil
			},
		}

		// The user supplies --sort explicitly.
		if err := cmd.Run(context.Background(), []string{"test", "--sort", "user_cli_value"}); err != nil {
			t.Fatalf("cmd.Run error: %v", err)
		}

		if recordedSort != "user_cli_value" {
			t.Errorf("sort = %q, want user_cli_value (user flag should not be overwritten)", recordedSort)
		}

		// port was omitted and still receives the configuration value.
		if recordedPort != 8080 {
			t.Errorf("port = %d, want 8080", recordedPort)
		}
	})
}

type urfaveSliceConfig struct {
	Items []string `flag:"items"`
}

func TestUrfaveBridgeSliceFlag(t *testing.T) {
	t.Parallel()

	cfg := urfaveSliceConfig{
		Items: []string{"first", "second", "third"},
	}

	var recordedItems []string

	cmd := &cli.Command{
		Name: "test",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:  "items",
				Value: []string{"default"},
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			if err := strataurfave.Apply(c, cfg); err != nil {
				return fmt.Errorf("apply configuration: %w", err)
			}

			recordedItems = c.StringSlice("items")

			return nil
		},
	}

	if err := cmd.Run(context.Background(), []string{"test"}); err != nil {
		t.Fatalf("cmd.Run error: %v", err)
	}

	if len(recordedItems) != 3 || recordedItems[0] != "first" || recordedItems[1] != "second" || recordedItems[2] != "third" {
		t.Errorf("recordedItems = %v, want [first second third]", recordedItems)
	}
}

type urfavePointerConfig struct {
	Port    *int    `flag:"port"`
	Missing *string `flag:"missing"`
}

func TestUrfaveBridgePointers(t *testing.T) {
	t.Parallel()

	portVal := 7070
	cfg := urfavePointerConfig{
		Port:    &portVal,
		Missing: nil,
	}

	var (
		recordedPort    int
		recordedMissing string
	)

	cmd := &cli.Command{
		Name: "test",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "port",
				Value: 3000,
			},
			&cli.StringFlag{
				Name:  "missing",
				Value: "default_str",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			if err := strataurfave.Apply(c, cfg); err != nil {
				return fmt.Errorf("apply configuration: %w", err)
			}

			recordedPort = c.Int("port")
			recordedMissing = c.String("missing")

			return nil
		},
	}

	if err := cmd.Run(context.Background(), []string{"test"}); err != nil {
		t.Fatalf("cmd.Run error: %v", err)
	}

	if recordedPort != 7070 {
		t.Errorf("recordedPort = %d, want 7070", recordedPort)
	}

	if recordedMissing != "default_str" {
		t.Errorf("recordedMissing = %q, want default_str", recordedMissing)
	}
}

type (
	myUrfaveInt    int
	myUrfaveString string
)

type customSliceUrfaveConfig struct {
	Ints    []myUrfaveInt    `flag:"ints"`
	Strings []myUrfaveString `flag:"strings"`
}

func TestCustomSliceTypesUrfave(t *testing.T) {
	t.Parallel()

	cfg := &customSliceUrfaveConfig{
		Ints:    []myUrfaveInt{1, 2},
		Strings: []myUrfaveString{"a", "b"},
	}

	flags, err := strataurfave.GenerateFlags(cfg)
	if err != nil {
		t.Fatalf("GenerateFlags with custom slice defaults failed: %v", err)
	}

	cmd := &cli.Command{
		Name:  "app",
		Flags: flags,
	}

	if err := cmd.Run(context.Background(), []string{"app", "--ints", "10", "--ints", "20", "--strings", "x", "--strings", "y"}); err != nil {
		t.Fatalf("cmd.Run error: %v", err)
	}

	if err := strataurfave.SyncFlagsToStruct(cmd, cfg); err != nil {
		t.Fatalf("SyncFlagsToStruct failed for custom slice types: %v", err)
	}

	if len(cfg.Ints) != 2 || cfg.Ints[0] != 10 || cfg.Ints[1] != 20 {
		t.Errorf("cfg.Ints = %v, want [10 20]", cfg.Ints)
	}

	if len(cfg.Strings) != 2 || cfg.Strings[0] != "x" || cfg.Strings[1] != "y" {
		t.Errorf("cfg.Strings = %v, want [x y]", cfg.Strings)
	}
}

func TestApplyWithCustomSliceTypes(t *testing.T) {
	t.Parallel()

	cfg := customSliceUrfaveConfig{
		Ints:    []myUrfaveInt{1, 2},
		Strings: []myUrfaveString{"a", "b"},
	}

	flags, err := strataurfave.GenerateFlags(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	cmd := &cli.Command{
		Name:  "app",
		Flags: flags,
	}

	if err := strataurfave.Apply(cmd, cfg); err != nil {
		t.Fatalf("Apply failed for custom slice types: %v", err)
	}
}
