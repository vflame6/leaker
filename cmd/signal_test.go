//go:build !windows

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestSignalContext_SecondSignalExits130 runs a helper process whose "run"
// ignores cancellation: the first SIGINT cancels the context, the second
// exits at once with code 130.
func TestSignalContext_SecondSignalExits130(t *testing.T) {
	if os.Getenv("LEAKER_SIGNAL_HELPER") == "1" {
		ctx, stop := signalContext()
		defer stop()
		fmt.Println("ready")
		<-ctx.Done()
		fmt.Println("cancelled")
		time.Sleep(time.Minute) // a run that does not stop
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalContext_SecondSignalExits130$")
	cmd.Env = append(os.Environ(), "LEAKER_SIGNAL_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	lines := bufio.NewScanner(stdout)
	expect := func(want string) {
		t.Helper()
		for lines.Scan() {
			if lines.Text() == want {
				return
			}
		}
		t.Fatalf("helper never printed %q", want)
	}

	expect("ready")
	_ = cmd.Process.Signal(syscall.SIGINT)
	expect("cancelled")
	_ = cmd.Process.Signal(syscall.SIGINT)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 130 {
			t.Fatalf("helper ended with %v, want exit code 130", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second SIGINT did not exit the process")
	}
}
