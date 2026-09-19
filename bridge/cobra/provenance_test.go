package stratacobra_test

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/zigai/strata"
	stratacobra "github.com/zigai/strata/bridge/cobra"
)

// derivedKeySettings carries a flag tag but no configuration tag, and its key is
// derived from the field name.
type derivedKeySettings struct {
	ServerPort int `flag:"server-port" usage:"Server port"`
}

// TestFlagProvenanceUsesTheSameKeyAsOtherTiers verifies that flag provenance
// lands under the key the other tiers use. The bridge once derived a kebab-case
// key from the field name while every other tier derived snake_case, and
// meta.Where("server_port") missed the recorded flag origin.
func TestFlagProvenanceUsesTheSameKeyAsOtherTiers(t *testing.T) {
	t.Parallel()

	meta := strata.NewMetadata()

	cmd := &cobra.Command{Use: commandName}
	if err := stratacobra.RegisterFlags(cmd, &derivedKeySettings{}); err != nil {
		t.Fatalf("RegisterFlags: %v", err)
	}

	if err := cmd.Flags().Set("server-port", "9090"); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	var settings derivedKeySettings
	if err := stratacobra.SyncFlagsToStruct(cmd, &settings, stratacobra.WithMetadata(meta)); err != nil {
		t.Fatalf("SyncFlagsToStruct: %v", err)
	}

	if settings.ServerPort != 9090 {
		t.Fatalf("ServerPort = %d, want 9090", settings.ServerPort)
	}

	origin, ok := meta.Where("server_port")
	if !ok {
		t.Fatal("Where(server_port) missed the flag origin")
	}

	if origin.Source != strata.SourceFlag {
		t.Errorf("origin source = %q, want %q", origin.Source, strata.SourceFlag)
	}
}

// TestBareSecretTagIsHonored verifies that a bare "secret" directive is
// honored. The bridge's own copy of the tag walker once matched "secret" only as
// a trailing option, while the shared walker also accepts it alone.
func TestBareSecretTagIsHonored(t *testing.T) {
	t.Parallel()

	cfg := struct {
		Token string `flag:"token" usage:"API token" strata:"secret"`
	}{Token: "leaked-default"}

	cmd := &cobra.Command{Use: commandName}
	if err := stratacobra.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("RegisterFlags: %v", err)
	}

	flag := cmd.Flags().Lookup("token")
	if flag == nil {
		t.Fatal("--token must be registered")
	}

	if flag.DefValue != "" {
		t.Fatalf("DefValue = %q, want empty: a secret default must not be published", flag.DefValue)
	}
}
