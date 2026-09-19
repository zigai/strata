package strata_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/strata"
)

type BenchConfig struct {
	AppName    string          `strata:"app_name"`
	Host       string          `strata:"host"`
	Port       int             `strata:"port"`
	Timeout    strata.Duration `strata:"timeout"`
	MaxRetries int             `strata:"max_retries"`
	Debug      bool            `strata:"debug"`
	Rate       float64         `strata:"rate"`
	Features   []string        `strata:"features"`
}

func (b *BenchConfig) SetDefaults() {
	b.AppName = "bench-app"
	b.Host = "127.0.0.1"
	b.Port = 8080
	b.Timeout = strata.Duration(10 * time.Second)
	b.MaxRetries = 5
	b.Debug = true
	b.Rate = 99.9
	b.Features = []string{"metrics", "tracing"}
}

func BenchmarkLoadWithoutFiles(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_, _, err := strata.Load[BenchConfig](strata.WithoutFiles())
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadWithEnv(b *testing.B) {
	b.Setenv("BENCH_APP_NAME", "custom-bench")
	b.Setenv("BENCH_PORT", "9090")
	b.Setenv("BENCH_TIMEOUT", "7d")

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_, _, err := strata.Load[BenchConfig](
			strata.WithEnvPrefix("BENCH_"),
			strata.WithoutFiles(),
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadFileTOML(b *testing.B) {
	tmpDir := b.TempDir()
	tomlPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`app_name = "file-app"
host = "0.0.0.0"
port = 7777
timeout = "30s"
max_retries = 10
debug = false
rate = 50.5
features = ["auth", "db"]
`)
	if err := os.WriteFile(tomlPath, content, 0o600); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_, _, err := strata.Load[BenchConfig](strata.WithExplicitPath(tomlPath))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseDuration(b *testing.B) {
	cases := []string{"300ms", "5s", "1h30m", "7d", "2w", "1w2d"}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		for _, c := range cases {
			_, err := strata.ParseDuration(c)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkSetBytesTOML(b *testing.B) {
	raw := []byte(`# Configuration
[server]
host = "127.0.0.1"
port = 8080 # comment
`)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_, err := strata.SetBytes(".toml", raw, "server.port", 9090)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSetBytesYAML(b *testing.B) {
	raw := []byte(`# Configuration
server:
  host: 127.0.0.1
  port: 8080 # comment
`)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_, err := strata.SetBytes(".yaml", raw, "server.port", 9090)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSetBytesJSON(b *testing.B) {
	raw := []byte(`{
  "server": {
    "host": "127.0.0.1",
    "port": 8080
  }
}
`)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_, err := strata.SetBytes(".json", raw, "server.port", 9090)
		if err != nil {
			b.Fatal(err)
		}
	}
}
