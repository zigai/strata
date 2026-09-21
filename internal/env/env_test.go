package env_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata/internal/env"
)

type customIP string

func (ip *customIP) UnmarshalText(text []byte) error {
	*ip = customIP("parsed:" + string(text))
	return nil
}

type nestedDB struct {
	Port     int           `strata:"port"`
	Timeout  time.Duration `strata:"timeout"`
	PoolSize int           `strata:"pool_size"`
}

type envTestConfig struct {
	AppName     string   `strata:"app_name"`
	Database    nestedDB `strata:"database"`
	Debug       bool     `strata:"debug"`
	Features    []string `strata:"features"`
	IP          customIP `strata:"ip"`
	CustomAlias string   `env:"OVERRIDE_ALIAS"`
}

func (e *envTestConfig) SetDefaults() {
	e.AppName = "default-app"
	e.Debug = false
	e.Database.Port = 5432
	e.Database.Timeout = 10 * time.Second
	e.Database.PoolSize = 5
	e.Features = []string{"auth"}
}

func TestEnvBindingVariables(t *testing.T) {
	t.Setenv("TAPP_DATABASE__PORT", "9999")
	t.Setenv("TAPP_DATABASE_PORT", "1111")
	t.Setenv("TAPP_DATABASE__TIMEOUT", "7h")
	t.Setenv("TAPP_DATABASE__POOL_SIZE", "50")
	t.Setenv("TAPP_DEBUG", "yes")
	t.Setenv("TAPP_FEATURES", "metrics, tracing, logs")
	t.Setenv("TAPP_IP", "10.0.0.1")
	t.Setenv("OVERRIDE_ALIAS", "aliased-value")
	t.Setenv("OTHER_PORT", "2222")

	var c envTestConfig
	c.SetDefaults()

	recorded := make(map[string]string)
	onBind := func(k, envVar, raw string) {
		recorded[k] = raw
	}

	err := env.Apply(&c, env.Options{
		Prefix: "TAPP_",
		OnBind: onBind,
	})
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}

	if c.Database.Port != 9999 {
		t.Errorf("Database.Port = %d, want 9999", c.Database.Port)
	}

	if c.Database.PoolSize != 50 {
		t.Errorf("Database.PoolSize = %d, want 50", c.Database.PoolSize)
	}

	if c.Database.Timeout != 7*time.Hour {
		t.Errorf("Database.Timeout = %v, want 7h", c.Database.Timeout)
	}

	if !c.Debug {
		t.Errorf("Debug = false, want true")
	}

	wantFeatures := []string{"metrics", "tracing", "logs"}
	if len(c.Features) != len(wantFeatures) {
		t.Fatalf("Features = %v, want %v", c.Features, wantFeatures)
	}

	for i, f := range c.Features {
		if f != wantFeatures[i] {
			t.Errorf("Features[%d] = %q, want %q", i, f, wantFeatures[i])
		}
	}

	if string(c.IP) != "parsed:10.0.0.1" {
		t.Errorf("IP = %q, want parsed:10.0.0.1", string(c.IP))
	}

	if c.CustomAlias != "aliased-value" {
		t.Errorf("CustomAlias = %q, want aliased-value", c.CustomAlias)
	}

	if c.AppName != "default-app" {
		t.Errorf("AppName = %q, want default-app", c.AppName)
	}
}

type BaseConfig struct {
	Host string `strata:"host"`
}

type CombinedConfig struct {
	BaseConfig

	Port int `strata:"port"`
}

func TestEmbeddedFieldPromotionEnv(t *testing.T) {
	t.Setenv("EMB_HOST", "promoted-host")
	t.Setenv("EMB_PORT", "8888")

	var cfg CombinedConfig

	err := env.Apply(&cfg, env.Options{
		Prefix: "EMB_",
	})
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}

	if cfg.Host != "promoted-host" {
		t.Errorf("Host = %q, want promoted-host", cfg.Host)
	}

	if cfg.Port != 8888 {
		t.Errorf("Port = %d, want 8888", cfg.Port)
	}
}

type OptionalDB struct {
	Host string `strata:"host"`
}

type AppWithOptionalDB struct {
	Name     string      `strata:"name"`
	Database *OptionalDB `strata:"database"`
}

func TestNilStructPointerRemainsNilWhenNoEnv(t *testing.T) {
	t.Setenv("NOOP_NAME", "my-service")

	var cfg AppWithOptionalDB

	err := env.Apply(&cfg, env.Options{
		Prefix: "NOOP_",
	})
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}

	if cfg.Name != "my-service" {
		t.Errorf("Name = %q, want my-service", cfg.Name)
	}

	if cfg.Database != nil {
		t.Errorf("Database = %+v, want nil", cfg.Database)
	}
}

type NetworkConfig struct {
	Address netip.Addr `strata:"address"`
}

func TestNetIPAddrEnv(t *testing.T) {
	t.Setenv("NET_ADDRESS", "192.168.1.50")

	var cfg NetworkConfig

	err := env.Apply(&cfg, env.Options{
		Prefix: "NET_",
	})
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}

	if !cfg.Address.IsValid() || cfg.Address.String() != "192.168.1.50" {
		t.Errorf("Address = %v, want 192.168.1.50", cfg.Address)
	}
}

type TimeAndBytesConfig struct {
	Timestamp *time.Time `strata:"timestamp"`
	Payload   []byte     `strata:"payload"`
}

func TestTimePointerAndByteSliceEnv(t *testing.T) {
	t.Setenv("TAB_TIMESTAMP", "2026-09-15T20:00:00Z")
	t.Setenv("TAB_PAYLOAD", "binary-string-content")

	var cfg TimeAndBytesConfig

	err := env.Apply(&cfg, env.Options{
		Prefix: "TAB_",
	})
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}

	if cfg.Timestamp == nil || cfg.Timestamp.Format(time.RFC3339) != "2026-09-15T20:00:00Z" {
		t.Errorf("Timestamp = %v, want 2026-09-15T20:00:00Z", cfg.Timestamp)
	}

	if string(cfg.Payload) != "binary-string-content" {
		t.Errorf("Payload = %q, want 'binary-string-content'", string(cfg.Payload))
	}
}

type SecretChildConfig struct {
	Port int `strata:"port"`
}

type SecretParentConfig struct {
	Database SecretChildConfig `strata:"database,secret"`
}

func TestInheritedSecretRedactionEnv(t *testing.T) {
	t.Setenv("SEC_DATABASE__PORT", "super_secret_password_123")

	var cfg SecretParentConfig

	err := env.Apply(&cfg, env.Options{
		Prefix: "SEC_",
	})
	if err == nil {
		t.Fatalf("expected error for invalid port, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in error, got: %s", errStr)
	}

	if strings.Contains(errStr, "super_secret_password_123") {
		t.Errorf("secret password was leaked in error message: %s", errStr)
	}
}

type recursiveNode struct {
	Value int
	Next  *recursiveNode
}

func TestEnvironmentRecursiveType(t *testing.T) {
	t.Parallel()

	var cfg recursiveNode
	lookups := 0
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		_ = env.Apply(&cfg, env.Options{Lookup: func(name string) (string, bool) {
			lookups++
			if lookups == 40 {
				panic("probe safety guard: 40 lookups reached with no matching environment variables")
			}
			return "", false
		}})
	}()
	if panicked != nil {
		t.Errorf("environment traversal did not terminate; %v", panicked)
	}
}

func TestExplicitEnvExclusion(t *testing.T) {
	t.Parallel()

	cfg := struct {
		Token string `env:"-"`
	}{Token: "original"}
	err := env.Apply(&cfg, env.Options{Lookup: func(name string) (string, bool) { return "unexpected", name == "TOKEN" }})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "original" {
		t.Errorf("env:\"-\" still binds the derived variable: %q", cfg.Token)
	}
}
