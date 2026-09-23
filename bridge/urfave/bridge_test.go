package strataurfave_test

import (
	"context"
	"testing"

	"github.com/urfave/cli/v3"

	strataurfave "github.com/zigai/strata/bridge/urfave"
)

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
