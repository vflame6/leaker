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
	"regexp"
	"runtime"
	"slices"
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
	result := runLeaker(t, "sources", "--no-color")
	if result.code != 0 {
		t.Fatalf("leaker sources exited %d\nstdout:\n%s\nstderr:\n%s", result.code, result.stdout, result.stderr)
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

// TestCLI_SourcesCommandListsKeyStateTypesLimits: "leaker sources" shows, for
// every source, whether its key is set, what it can search and its limits.
func TestCLI_SourcesCommandListsKeyStateTypesLimits(t *testing.T) {
	config := filepath.Join(t.TempDir(), "keys.yaml")
	if err := os.WriteFile(config, []byte("dehashed: [k1]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := runLeaker(t, "sources", "-p", config, "--no-color")
	if result.code != 0 {
		t.Fatalf("leaker sources exited %d\nstderr:\n%s", result.code, result.stderr)
	}
	rows := map[string]string{
		"dehashed":   `configured +email username domain keyword phone +100ms +10000 +10000`,
		"hudsonrock": `optional +email username domain +100ms`,
		"hibr":       `missing +domain +1s`,
		"proxynova":  `not needed +email username domain keyword phone +667ms +100 +100`,
		"local":      `- +email username domain keyword phone +-`,
	}
	for name, cells := range rows {
		if !regexp.MustCompile(`(?m)^` + name + ` +` + cells).MatchString(result.stdout) {
			t.Errorf("no %s row matching %q in:\n%s", name, cells, result.stdout)
		}
	}
	if strings.Contains(result.stdout+result.stderr, "Made by") {
		t.Error("the sources list prints no banner")
	}
}

// TestCLI_LimitsMustBePositive: a zero or negative limit is a usage error.
func TestCLI_LimitsMustBePositive(t *testing.T) {
	for _, flag := range []string{"--max-results", "--max-requests", "--threads"} {
		result := runLeaker(t, "email", "jane@example.com", flag, "0", "--no-color")
		if result.code != 2 || !strings.Contains(result.stderr, flag) {
			t.Errorf("%s 0: exit code %d, want 2 and the flag named\nstderr:\n%s", flag, result.code, result.stderr)
		}
	}
}

// TestCLI_RemovedFlagsAreUnknown: -V (HIBP verification) and -L (replaced by
// "leaker sources") are gone.
func TestCLI_RemovedFlagsAreUnknown(t *testing.T) {
	// Should a flag still parse, the refusing proxy keeps the search local.
	proxy := fakeapi.NewProxy(t)
	for _, args := range [][]string{
		{"email", "jane@example.com", "-V"}, {"email", "jane@example.com", "--verify"},
		{"-L"}, {"--list-sources"},
	} {
		result := runLeaker(t, append(args, "--no-color", "--proxy", proxy.URL)...)
		if result.code != 2 || !strings.Contains(result.stderr, "unknown flag") {
			t.Errorf("leaker %v: exit code %d, want 2 for an unknown flag\nstderr:\n%s", args, result.code, result.stderr)
		}
	}
}

// TestCLI_StatsFlagPrintsTheTable: the per-source table is printed with
// --stats only.
func TestCLI_StatsFlagPrintsTheTable(t *testing.T) {
	proxy := fakeapi.NewProxy(t) // refuses every request
	for _, stats := range []bool{false, true} {
		args := []string{"email", "jane@example.com", "-s", "proxynova", "--no-color", "--proxy", proxy.URL}
		if stats {
			args = append(args, "--stats")
		}
		result := runLeaker(t, args...)
		if got := strings.Contains(result.stderr, "SOURCE"); got != stats {
			t.Errorf("--stats %v: table printed = %v\nstderr:\n%s", stats, got, result.stderr)
		}
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
	want := "4 of 17 sources active; 13 skipped (no key)"
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
// the sources.
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

// TestCLI_CompletionPrintsOnlyTheScript: the script goes to stdout without the
// banner, and printing it creates no config file.
func TestCLI_CompletionPrintsOnlyTheScript(t *testing.T) {
	config := filepath.Join(t.TempDir(), "provider-config.yaml")
	firstLines := map[string]string{
		"bash": "# bash completion for leaker",
		"zsh":  "#compdef leaker",
		"fish": "# fish completion for leaker",
	}
	for shell, first := range firstLines {
		result := runLeakerWith(t, cliOptions{
			args: []string{"completion", shell},
			env:  []string{"LEAKER_PROVIDER_CONFIG=" + config},
		})
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("leaker completion %s exited %d\nstderr:\n%s", shell, result.code, result.stderr)
		}
		if !strings.HasPrefix(result.stdout, first+"\n") {
			t.Errorf("leaker completion %s: output starts with %q, want %q", shell, strings.SplitN(result.stdout, "\n", 2)[0], first)
		}
	}
	if _, err := os.Stat(config); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("printing a completion script created the provider config (stat: %v)", err)
	}
}

func TestCLI_CompletionUnknownShellExitsTwo(t *testing.T) {
	result := runLeaker(t, "completion", "powershell")
	if result.code != 2 || !strings.Contains(result.stderr, `"powershell"`) {
		t.Fatalf("leaker completion powershell exited %d, want 2 naming the shell\nstderr:\n%s", result.code, result.stderr)
	}
}

// TestCLI_ManPrintsTheManPage: "leaker man" (hidden from --help) prints roff
// for the release packages.
func TestCLI_ManPrintsTheManPage(t *testing.T) {
	result := runLeaker(t, "man")
	if result.code != 0 || result.stderr != "" {
		t.Fatalf("leaker man exited %d\nstderr:\n%s", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, "\n.TH LEAKER 1 ") || strings.Contains(result.stdout, "Made by") {
		t.Errorf("leaker man printed:\n%s", result.stdout)
	}

	help := runLeaker(t, "--help")
	for line := range strings.SplitSeq(help.stdout, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == "man" {
			t.Errorf("--help lists the hidden man command: %q", line)
		}
	}
	if !strings.Contains(help.stdout, "completion") {
		t.Errorf("--help does not list the completion command:\n%s", help.stdout)
	}
}

// TestCLI_BashCompletionLoadsAsDocumented runs the load command the bash
// script's header recommends, on every bash available (macOS /bin/bash 3.2
// ignores "source <(...)").
func TestCLI_BashCompletionLoadsAsDocumented(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash completion runs on Unix")
	}
	script := runLeaker(t, "completion", "bash").stdout
	_, rest, found := strings.Cut(script, "Load it in the current shell with\n#   ")
	load, _, _ := strings.Cut(rest, "\n")
	if !found || load == "" {
		t.Fatalf("the bash script does not document how to load it:\n%s", script)
	}

	var shells []string
	if path, err := exec.LookPath("bash"); err == nil {
		shells = append(shells, path)
	}
	if _, err := os.Stat("/bin/bash"); err == nil && !slices.Contains(shells, "/bin/bash") {
		shells = append(shells, "/bin/bash")
	}
	if len(shells) == 0 {
		t.Skip("bash is not installed")
	}
	for _, bash := range shells {
		cmd := exec.Command(bash, "--norc", "--noprofile", "-c", load+"; complete -p leaker")
		cmd.Env = append(testEnv(t), "PATH="+filepath.Dir(leakerBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "_leaker") {
			t.Errorf("%s: %q did not register the completion: %v\n%s", bash, load, err, out)
		}
	}
}
