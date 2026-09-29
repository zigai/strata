//go:build unix

package cascade_test

import (
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/zigai/strata/internal/cascade"
)

// A tier entry named config.<ext> that is not a regular file is skipped. A
// FIFO would block the open forever, so the discovery runs under a deadline.
func TestDiscoverSkipsNonRegularTierFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(path string) error
	}{
		{"directory", func(path string) error { return os.Mkdir(path, 0o700) }},
		{"fifo", func(path string) error { return syscall.Mkfifo(path, 0o600) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			systemBase, userBase := isolateTiers(t)

			for _, base := range []string{systemBase, userBase} {
				dir := filepath.Join(base, "testapp")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}

				if err := tc.make(filepath.Join(dir, "config.toml")); err != nil {
					t.Fatalf("create %s: %v", tc.name, err)
				}
			}

			userYAML := writeTierFile(t, userBase, "config.yaml")

			type result struct {
				layers []cascade.Layer
				err    error
			}

			done := make(chan result, 1)

			go func() {
				layers, err := cascade.Discover(cascade.Params{AppName: "testapp", Extensions: []string{".toml", ".yaml"}})
				done <- result{layers, err}
			}()

			select {
			case got := <-done:
				want := []cascade.Layer{{Source: cascade.SourceUser, Path: userYAML}}
				if got.err != nil || !reflect.DeepEqual(got.layers, want) {
					t.Fatalf("Discover = %+v, %v; want %+v", got.layers, got.err, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Discover blocked on a non-regular config.toml")
			}
		})
	}
}
