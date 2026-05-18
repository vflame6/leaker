package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRootVersionCommandPrintsVersion(t *testing.T) {
	result := runLeaker(t, "--version")
	if result.err != nil {
		t.Fatalf("go run . --version failed: %v\nstdout:\n%s\nstderr:\n%s", result.err, result.stdout, result.stderr)
	}
	if strings.TrimSpace(result.stdout) != "dev" {
		t.Fatalf("expected dev version output, got stdout=%q stderr=%q", result.stdout, result.stderr)
	}
}

func TestRootHelpCommandShowsUsageWithoutBanner(t *testing.T) {
	result := runLeaker(t, "--help")
	if result.err != nil {
		t.Fatalf("go run . --help failed: %v\nstdout:\n%s\nstderr:\n%s", result.err, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "Usage: leaker <command> [flags]") {
		t.Fatalf("expected root usage in help output, got stdout=%q", result.stdout)
	}
	if strings.Contains(result.stdout, "Made by") {
		t.Fatalf("did not expect banner in help output, got stdout=%q", result.stdout)
	}
}

func TestRootListSourcesCommandStartsWithoutNetwork(t *testing.T) {
	result := runLeaker(t, "--list-sources", "--no-color")
	if result.err != nil {
		t.Fatalf("go run . --list-sources --no-color failed: %v\nstdout:\n%s\nstderr:\n%s", result.err, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "Source groups:") {
		t.Fatalf("expected source groups in output, got stdout=%q", result.stdout)
	}
	if !strings.Contains(result.stdout, "Available sources:") {
		t.Fatalf("expected available sources in output, got stdout=%q", result.stdout)
	}
	if !strings.Contains(result.stdout, "local") {
		t.Fatalf("expected local source in output, got stdout=%q", result.stdout)
	}
}

type commandResult struct {
	stdout string
	stderr string
	err    error
}

func runLeaker(t *testing.T, args ...string) commandResult {
	t.Helper()

	cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
	cmd.Env = append(os.Environ(),
		"LEAKER_DB="+t.TempDir()+"/leaker.db",
		"LEAKER_PROVIDER_CONFIG="+t.TempDir()+"/provider-config.yaml",
	)

	stdout, err := cmd.Output()
	stderr := ""
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = string(exitErr.Stderr)
	}

	return commandResult{stdout: string(stdout), stderr: stderr, err: err}
}
