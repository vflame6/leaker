package cmd

import (
	"context"
	"errors"

	"github.com/vflame6/leaker/runner"
)

// Exit codes of the leaker command.
const (
	ExitOK          = 0   // the run finished, with or without leaks
	ExitError       = 1   // runtime error
	ExitUsage       = 2   // usage or configuration error, nothing was searched
	ExitInterrupted = 130 // interrupted by SIGINT or SIGTERM
)

// kongUsageError is the exit code kong uses for command-line parse errors.
const kongUsageError = 80

// exitCode maps the error that ended a run to the process exit code.
func exitCode(err error) int {
	var cfgErr *runner.ConfigError
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, context.Canceled):
		return ExitInterrupted
	case errors.As(err, &cfgErr):
		return ExitUsage
	default:
		return ExitError
	}
}

// usageExitCode maps kong's exit codes to leaker's: parse errors exit with 2.
func usageExitCode(code int) int {
	if code == kongUsageError {
		return ExitUsage
	}
	return code
}
