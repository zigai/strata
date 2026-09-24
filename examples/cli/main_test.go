package main

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"
)

// The README points readers at this example, so it walks the workflow the
// example exists to show: flags, config init/set/show, a rejected typo, and a
// bad value reported with the file that set it.
func TestExampleWorkflow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())

	if out := mustRun(t, "serve", "-p", "9000"); !strings.Contains(out, "on :9000") {
		t.Fatalf("serve -p 9000 printed %q", out)
	}

	path := strings.TrimPrefix(strings.TrimSpace(mustRun(t, "config", "init")), "wrote ")
	mustRun(t, "config", "set", "port", "9001")

	if out := mustRun(t, "config", "show"); !regexp.MustCompile(`port\s+9001\s+user ` + regexp.QuoteMeta(path)).MatchString(out) {
		t.Fatalf("config show lacks port 9001 from %s:\n%s", path, out)
	}

	if _, err := run(t, "config", "set", "prot", "1"); err == nil || !strings.Contains(err.Error(), `did you mean "port"`) {
		t.Fatalf("config set prot: err = %v, want a suggestion", err)
	}

	mustRun(t, "config", "set", "port", "0")

	_, err := run(t, "serve")
	if err == nil || !strings.Contains(err.Error(), "must be between 1 and 65535") || !strings.Contains(err.Error(), path) {
		t.Fatalf("serve with port 0: err = %v, want the range error naming %s", err, path)
	}
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	root := newRootCommand()
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	err := root.Execute()

	return out.String(), err
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()

	out, err := run(t, args...)
	if err != nil {
		t.Fatalf("myapp %s: %v", strings.Join(args, " "), err)
	}

	return out
}
