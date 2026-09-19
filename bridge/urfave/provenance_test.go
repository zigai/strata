package strataurfave_test

import (
	"context"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/zigai/strata"
	strataurfave "github.com/zigai/strata/bridge/urfave"
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
	cfg := derivedKeySettings{}

	cmd := command(func(ctx context.Context, c *cli.Command) (context.Context, error) {
		return ctx, strataurfave.SyncFlagsToStruct(c, &cfg, strataurfave.WithMetadata(meta))
	})

	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("RegisterFlags: %v", err)
	}

	if err := cmd.Run(context.Background(), []string{commandName, "--server-port", "9090"}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if cfg.ServerPort != 9090 {
		t.Fatalf("ServerPort = %d, want 9090", cfg.ServerPort)
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

	cmd := &cli.Command{Name: commandName}
	if err := strataurfave.RegisterFlags(cmd, &cfg); err != nil {
		t.Fatalf("RegisterFlags: %v", err)
	}

	flag := flagNamed(cmd, "token")
	if flag == nil {
		t.Fatal("--token must be registered")
	}

	documented, ok := flag.(cli.DocGenerationFlag)
	if !ok {
		t.Fatal("--token must support help generation")
	}

	if documented.IsDefaultVisible() {
		t.Error("--token must not publish its default")
	}
}
