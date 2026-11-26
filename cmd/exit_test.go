package cmd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/vflame6/leaker/runner"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, 0},
		{"runtime error", errors.New("cannot open local DB"), 1},
		{"config error", &runner.ConfigError{Err: errors.New("no API key configured for snusbase")}, 2},
		{"wrapped config error", fmt.Errorf("startup: %w", &runner.ConfigError{Err: errors.New("bad")}), 2},
		{"interrupted", context.Canceled, 130},
		{"interrupted among other errors", errors.Join(errors.New("x"), context.Canceled), 130},
	}
	for _, c := range cases {
		if got := exitCode(c.err); got != c.want {
			t.Errorf("%s: exitCode(%v) = %d, want %d", c.name, c.err, got, c.want)
		}
	}
}

func TestUsageExitCode(t *testing.T) {
	if got := usageExitCode(80); got != 2 {
		t.Errorf("kong usage error 80 maps to %d, want 2", got)
	}
	for _, code := range []int{0, 1} {
		if got := usageExitCode(code); got != code {
			t.Errorf("usageExitCode(%d) = %d, want unchanged", code, got)
		}
	}
}
