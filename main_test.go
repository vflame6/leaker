package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
	"github.com/vflame6/leaker/runner"
	"github.com/vflame6/leaker/runner/sources"
)

// leakerBin is the leaker binary built once for the CLI tests.
var leakerBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "leaker-cli-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	leakerBin = filepath.Join(dir, "leaker")
	if runtime.GOOS == "windows" {
		leakerBin += ".exe"
	}
	// -buildvcs=false keeps the version at "dev", as for any unreleased build.
	build := exec.Command("go", "build", "-buildvcs=false", "-o", leakerBin, ".")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building leaker:", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestRootVersionCommandPrintsVersion(t *testing.T) {
	result := runLeaker(t, "--version")
	if result.code != 0 {
		t.Fatalf("leaker --version exited %d\nstdout:\n%s\nstderr:\n%s", result.code, result.stdout, result.stderr)
	}
	if strings.TrimSpace(result.stdout) != "dev" {
		t.Fatalf("expected dev version output, got stdout=%q stderr=%q", result.stdout, result.stderr)
	}
}

func TestRootHelpCommandShowsUsageWithoutBanner(t *testing.T) {
	result := runLeaker(t, "--help")
	if result.code != 0 {
		t.Fatalf("leaker --help exited %d\nstdout:\n%s\nstderr:\n%s", result.code, result.stdout, result.stderr)
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
	if result.code != 0 {
		t.Fatalf("leaker --list-sources exited %d\nstdout:\n%s\nstderr:\n%s", result.code, result.stdout, result.stderr)
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

// TestCLI_UsageErrorExitsTwo: kong's own usage errors use exit code 2 too.
func TestCLI_UsageErrorExitsTwo(t *testing.T) {
	result := runLeaker(t, "email", "--no-such-flag")
	if result.code != 2 {
		t.Fatalf("exit code %d, want 2\nstderr:\n%s", result.code, result.stderr)
	}
}

// TestCLI_NoKeyExitsTwo: sources named with -s but missing their keys stop the
// run with exit code 2, name the sources, and send nothing.
func TestCLI_NoKeyExitsTwo(t *testing.T) {
	proxy := fakeapi.NewProxy(t)
	result := runLeakerWith(t, cliOptions{
		args: []string{"email", "jane@example.com", "-s", "snusbase,intelx", "--no-color"},
		env:  []string{"HTTPS_PROXY=" + proxy.URL, "HTTP_PROXY=" + proxy.URL},
	})
	if result.code != 2 {
		t.Fatalf("exit code %d, want 2\nstderr:\n%s", result.code, result.stderr)
	}
	for _, name := range []string{"snusbase", "intelx"} {
		if !strings.Contains(result.stderr, name) {
			t.Errorf("stderr does not name %s:\n%s", name, result.stderr)
		}
	}
	if strings.Contains(result.stderr+result.stdout, "Found 0 leaks") {
		t.Errorf("a search that did not run must not report 0 leaks:\n%s", result.stderr)
	}
	if hosts := proxy.Hosts(); len(hosts) != 0 {
		t.Errorf("no request may be sent, proxy saw %v", hosts)
	}
}

type cliOptions struct {
	args  []string
	env   []string // extra KEY=VALUE entries
	stdin io.Reader
	// configure runs on the command before it starts.
	configure func(cmd *exec.Cmd)
}

type commandResult struct {
	stdout string
	stderr string
	code   int
}

func runLeaker(t *testing.T, args ...string) commandResult {
	t.Helper()
	return runLeakerWith(t, cliOptions{args: args})
}

// runLeakerWith runs the binary with its config and DB in a temp dir and no
// proxy from the developer's environment.
func runLeakerWith(t *testing.T, opts cliOptions) commandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, leakerBin, opts.args...)
	cmd.Env = append(testEnv(t), opts.env...)
	cmd.Stdin = opts.stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if opts.configure != nil {
		opts.configure(cmd)
	}

	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("running leaker: %v", err)
	}
	return commandResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// testEnv returns the environment for the binary: no proxy or leaker
// settings from the developer's shell, and private temp paths.
func testEnv(t *testing.T) []string {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "REQUEST_METHOD":
			continue
		}
		if strings.HasPrefix(name, "LEAKER_") {
			continue
		}
		env = append(env, kv)
	}
	dir := t.TempDir()
	return append(env,
		"LEAKER_DB="+filepath.Join(dir, "leaker.db"),
		"LEAKER_PROVIDER_CONFIG="+filepath.Join(dir, "provider-config.yaml"),
	)
}

// TestCLI_OnlineWithTwoKeysReportsActiveAndSkipped: the default group runs
// what it can and says how many sources were skipped.
func TestCLI_OnlineWithTwoKeysReportsActiveAndSkipped(t *testing.T) {
	proxy := fakeapi.NewProxy(t)
	config := filepath.Join(t.TempDir(), "keys.yaml")
	if err := os.WriteFile(config, []byte("leakcheck: [k1]\nsnusbase: [k2]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := runLeakerWith(t, cliOptions{
		args: []string{"email", "jane@example.com", "-p", config, "--no-color", "--proxy", proxy.URL},
	})
	// The fake proxy refuses every request, so the search fails: exit 1.
	if result.code != 1 {
		t.Fatalf("exit code %d, want 1\nstderr:\n%s", result.code, result.stderr)
	}
	want := "4 of 15 sources active; 11 skipped (no key)"
	if n := strings.Count(result.stderr, want); n != 1 {
		t.Errorf("want %q once on stderr, found %d:\n%s", want, n, result.stderr)
	}
}

// TestCLI_EnvironmentProxyUsedWithoutFlag: without --proxy, HTTPS_PROXY from
// the environment applies to the sources.
func TestCLI_EnvironmentProxyUsedWithoutFlag(t *testing.T) {
	envProxy := fakeapi.NewProxy(t)
	result := runLeakerWith(t, cliOptions{
		args: []string{"email", "jane@example.com", "-s", "proxynova", "--no-color"},
		env:  []string{"HTTPS_PROXY=" + envProxy.URL},
	})
	if result.code != 1 { // the fake proxy refuses the request
		t.Fatalf("exit code %d, want 1\nstderr:\n%s", result.code, result.stderr)
	}
	if hosts := envProxy.Hosts(); len(hosts) != 1 || hosts[0] != "api.proxynova.com" {
		t.Errorf("environment proxy saw %v, want [api.proxynova.com]", hosts)
	}
}

// TestCLI_ProxyFlagOverridesEnvironment: --proxy wins over HTTPS_PROXY for
// the sources. (The -V lookup is covered by TestProxyFlag_EveryHostUsesProxy:
// here no result ever reaches the verifier.)
func TestCLI_ProxyFlagOverridesEnvironment(t *testing.T) {
	flagProxy, envProxy := fakeapi.NewProxy(t), fakeapi.NewProxy(t)
	result := runLeakerWith(t, cliOptions{
		args: []string{"email", "jane@example.com", "-s", "proxynova", "--no-color", "--proxy", flagProxy.URL},
		env:  []string{"HTTPS_PROXY=" + envProxy.URL},
	})
	if result.code != 1 { // the fake proxy refuses the request
		t.Fatalf("exit code %d, want 1\nstderr:\n%s", result.code, result.stderr)
	}
	if hosts := flagProxy.Hosts(); len(hosts) != 1 || hosts[0] != "api.proxynova.com" {
		t.Errorf("--proxy saw %v, want [api.proxynova.com]", hosts)
	}
	if hosts := envProxy.Hosts(); len(hosts) != 0 {
		t.Errorf("environment proxy must be bypassed when --proxy is set, saw %v", hosts)
	}
}

// TestCLI_TwentyRunsPrintIdenticalOutput: the same records print the same
// bytes on every run, and JSON names the breach without -M.
func TestCLI_TwentyRunsPrintIdenticalOutput(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "seed.db")
	db, err := runner.OpenLeakerDB(dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	record := sources.Result{Source: "snusbase", Email: "jane@example.com", Password: "hunter2", Database: "ExampleDB"}
	for _, k := range []string{"stealer_family", "computer_name", "operating_system", "date_compromised", "log_date", "hostname"} {
		record.SetExtra(k, "value-"+k)
	}
	if err := db.Insert(&record); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	args := []string{"email", "jane@example.com", "-s", "local", "-q", "--no-write-db", "--db", dbPath}
	first := runLeaker(t, args...)
	if first.code != 0 || !strings.Contains(first.stdout, "jane@example.com") {
		t.Fatalf("exit %d, stdout %q, stderr %q", first.code, first.stdout, first.stderr)
	}
	for run := 1; run < 20; run++ {
		if again := runLeaker(t, args...); again.stdout != first.stdout {
			t.Fatalf("run %d printed\n%q\nfirst run printed\n%q", run, again.stdout, first.stdout)
		}
	}

	jsonRun := runLeaker(t, append(args, "-j")...)
	if !strings.Contains(jsonRun.stdout, `"database":"ExampleDB"`) {
		t.Errorf("JSON without -M lacks the database: %s", jsonRun.stdout)
	}
}

// TestCLI_AllSourcesFailingExitsOne: a search no source could run (here a
// dead proxy) is a runtime error, never "0 leaks", even with -q.
func TestCLI_AllSourcesFailingExitsOne(t *testing.T) {
	proxy := fakeapi.NewProxy(t)
	for _, quiet := range []bool{false, true} {
		args := []string{"email", "jane@example.com", "-s", "proxynova", "--no-color", "--proxy", proxy.URL}
		if quiet {
			args = append(args, "-q")
		}
		result := runLeakerWith(t, cliOptions{args: args})
		if result.code != 1 {
			t.Errorf("quiet=%v: exit code %d, want 1\nstderr:\n%s", quiet, result.code, result.stderr)
		}
		if strings.Contains(result.stderr, "Found 0 leaks") {
			t.Errorf("quiet=%v: reported 0 leaks for a search that failed:\n%s", quiet, result.stderr)
		}
		if !strings.Contains(result.stderr, "could not be searched") {
			t.Errorf("quiet=%v: the failure must be printed:\n%s", quiet, result.stderr)
		}
	}
}

// TestCLI_NothingToSearchExitsTwo: invalid targets, an unsupported scan type
// or a blank key mean nothing was searched: exit 2.
func TestCLI_NothingToSearchExitsTwo(t *testing.T) {
	config := filepath.Join(t.TempDir(), "keys.yaml")
	if err := os.WriteFile(config, []byte("hibr: [k]\nsnusbase: ['']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"email", "notanemail", "-s", "proxynova"},
		{"phone", "abc", "-s", "proxynova"},
		{"email", "jane@example.com", "-s", "hibr", "-p", config},
		{"email", "jane@example.com", "-s", "snusbase", "-p", config},
	} {
		if result := runLeaker(t, append(args, "--no-color")...); result.code != 2 {
			t.Errorf("leaker %s: exit code %d, want 2\nstderr:\n%s", strings.Join(args, " "), result.code, result.stderr)
		}
	}
}
