package runner

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/vflame6/leaker/logger"
)

func TestListSourcesQuietPrintsNamesOnly(t *testing.T) {
	var stdout bytes.Buffer
	var logs bytes.Buffer

	logger.SetOutput(&logs)
	t.Cleanup(func() {
		logger.SetOutput(os.Stderr)
		logger.SetMaxLevel(logger.LevelInfo)
		logger.SetNoColor(false)
	})

	ListSources(&Options{
		Output:         &stdout,
		ProviderConfig: "/tmp/provider-config.yaml",
		Quiet:          true,
	})

	if logs.Len() != 0 {
		t.Fatalf("expected quiet list-sources to suppress logger output, got %q", logs.String())
	}
	if strings.Contains(stdout.String(), "[INFO]") || strings.Contains(stdout.String(), "\x1b[") {
		t.Fatalf("expected names-only output, got %q", stdout.String())
	}
	if !regexp.MustCompile(`(?m)^intelx +missing +email username domain keyword phone +1s`).MatchString(stdout.String()) {
		t.Fatalf("expected an intelx row with its key state, types and interval, got %q", stdout.String())
	}
}

func TestListSourcesNoColorDisablesAnsiInLogs(t *testing.T) {
	var stdout bytes.Buffer
	var logs bytes.Buffer

	logger.SetOutput(&logs)
	t.Cleanup(func() {
		logger.SetOutput(os.Stderr)
		logger.SetMaxLevel(logger.LevelInfo)
		logger.SetNoColor(false)
	})

	ListSources(&Options{
		Output:         &stdout,
		ProviderConfig: "/tmp/provider-config.yaml",
		NoColor:        true,
	})

	if strings.Contains(logs.String(), "\x1b[") {
		t.Fatalf("expected non-colored logs, got %q", logs.String())
	}
	if !strings.Contains(logs.String(), "[INFO] ") {
		t.Fatalf("expected an info line in logs, got %q", logs.String())
	}
	if !strings.Contains(stdout.String(), "whiteintel") {
		t.Fatalf("expected source names in output, got %q", stdout.String())
	}
}
