package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommandShowsHelpByDefault(t *testing.T) {
	stdout, stderr, err := executeRootCommand()
	if err != nil {
		t.Fatalf("execute root command: %v", err)
	}

	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	if !strings.Contains(stdout, "Usage:") {
		t.Fatalf("stdout = %q, want Usage section", stdout)
	}

	if !strings.Contains(stdout, "strata [flags]") {
		t.Fatalf("stdout = %q, want command usage", stdout)
	}
}

func TestRootCommandShowsVersion(t *testing.T) {
	stdout, stderr, err := executeRootCommand("--version")
	if err != nil {
		t.Fatalf("execute root command: %v", err)
	}

	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	want := "strata dev (commit: none, built: unknown)\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func executeRootCommand(args ...string) (string, string, error) {
	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	rootCommand := newRootCommand()
	rootCommand.SetOut(&stdout)
	rootCommand.SetErr(&stderr)
	rootCommand.SetArgs(args)

	err := rootCommand.Execute()

	return stdout.String(), stderr.String(), err
}
