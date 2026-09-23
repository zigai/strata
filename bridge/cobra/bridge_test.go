package stratacobra_test

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/zigai/strata"
	stratacobra "github.com/zigai/strata/bridge/cobra"
)

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
