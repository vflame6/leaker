//go:build !windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
)

// syncBuffer is a bytes.Buffer safe to read while the process writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type runningLeaker struct {
	cmd            *exec.Cmd
	stdout, stderr *syncBuffer
	done           chan struct{}
	code           int
}

// startLeaker starts the binary with stdin connected to an open pipe that
// never sends anything, like a CI job or a script with an idle stdin.
func startLeaker(t *testing.T, args ...string) *runningLeaker {
	t.Helper()
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdinWrite.Close(); _ = stdinRead.Close() })

	r := &runningLeaker{stdout: &syncBuffer{}, stderr: &syncBuffer{}, done: make(chan struct{})}
	r.cmd = exec.Command(leakerBin, args...)
	r.cmd.Env = testEnv(t)
	r.cmd.Stdin = stdinRead
	r.cmd.Stdout, r.cmd.Stderr = r.stdout, r.stderr
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = r.cmd.Wait()
		r.code = r.cmd.ProcessState.ExitCode()
		close(r.done)
	}()
	t.Cleanup(func() {
		select {
		case <-r.done:
		default:
			_ = r.cmd.Process.Kill()
			<-r.done
		}
	})
	return r
}

// waitExit waits up to timeout for the process to exit.
func (r *runningLeaker) waitExit(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(timeout):
		t.Fatalf("leaker still running after %v\nstderr:\n%s", timeout, r.stderr)
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCLI_PositionalTargetDoesNotWaitForStdin: an open, idle stdin is not
// read when a target is given.
func TestCLI_PositionalTargetDoesNotWaitForStdin(t *testing.T) {
	proxy := fakeapi.NewProxy(t)
	r := startLeaker(t, "email", "jane@example.com", "-s", "proxynova", "--no-color", "--proxy", proxy.URL)
	r.waitExit(t, 20*time.Second)
	// The fake proxy refuses the request, so the only source fails: exit 1.
	if r.code != 1 {
		t.Fatalf("exit code %d, want 1\nstderr:\n%s", r.code, r.stderr)
	}
	if hosts := proxy.Hosts(); len(hosts) != 1 || hosts[0] != "api.proxynova.com" {
		t.Errorf("proxy saw %v, want one request to api.proxynova.com", hosts)
	}
}

// TestCLI_SigintWhileWaitingOnStdinExits130: waiting for targets on stdin
// does not block an interrupt.
func TestCLI_SigintWhileWaitingOnStdinExits130(t *testing.T) {
	r := startLeaker(t, "email", "--no-color")
	waitUntil(t, "startup", func() bool { return strings.Contains(r.stderr.String(), "Loading provider config") })
	time.Sleep(200 * time.Millisecond) // let it reach the read

	start := time.Now()
	if err := r.cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	r.waitExit(t, 5*time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("exited %v after SIGINT, want under 1s", elapsed)
	}
	if r.code != 130 {
		t.Errorf("exit code %d, want 130\nstderr:\n%s", r.code, r.stderr)
	}
}

// TestCLI_SigintDuringRunExits130Once: an interrupt during a 60-target run
// stops it with a single "Interrupted" line and exit code 130.
func TestCLI_SigintDuringRunExits130Once(t *testing.T) {
	proxy := fakeapi.NewProxy(t)
	var targets strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&targets, "user%d@example.com\n", i)
	}
	list := filepath.Join(t.TempDir(), "targets.txt")
	if err := os.WriteFile(list, []byte(targets.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	r := startLeaker(t, "email", "-l", list, "-s", "proxynova", "--no-color", "--proxy", proxy.URL)
	waitUntil(t, "the first request", func() bool { return len(proxy.Hosts()) > 0 })
	if err := r.cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	r.waitExit(t, 5*time.Second)

	stderr := r.stderr.String()
	if r.code != 130 {
		t.Errorf("exit code %d, want 130\nstderr:\n%s", r.code, stderr)
	}
	if n := strings.Count(stderr, "Interrupted"); n != 1 {
		t.Errorf("%d \"Interrupted\" lines, want 1\nstderr:\n%s", n, stderr)
	}
	if n := strings.Count(stderr, "Enumerating leaks for"); n > 2 {
		t.Errorf("%d targets started, want the run to stop at the interrupt", n)
	}
}
