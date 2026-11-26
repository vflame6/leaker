package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vflame6/leaker/runner/sources"
)

func runToFile(t *testing.T, out, target, password string, overwrite bool) error {
	t.Helper()
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.Targets = target
	r.options.OutputFile = out
	r.options.Overwrite = overwrite
	r.options.Output = &bytes.Buffer{}
	r.scanSources = []sources.Source{&fakeSource{name: "s", emits: []sources.Result{{Source: "s", Email: target, Password: password}}}}
	return r.RunEnumeration(context.Background())
}

// TestRunEnumeration_OverwriteKeepsOnlyLastRun: two runs with the same -o
// file and --overwrite leave only the second run's results.
func TestRunEnumeration_OverwriteKeepsOnlyLastRun(t *testing.T) {
	captureLogs(t)
	out := filepath.Join(t.TempDir(), "same.txt")
	if err := runToFile(t, out, "first@example.com", "one", true); err != nil {
		t.Fatal(err)
	}
	if err := runToFile(t, out, "second@example.com", "two", true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if want := "email:second@example.com, password:two\n"; string(data) != want {
		t.Errorf("file = %q, want %q", data, want)
	}
}

func TestRunEnumeration_ExistingOutputWithoutOverwriteIsConfigError(t *testing.T) {
	captureLogs(t)
	out := filepath.Join(t.TempDir(), "same.txt")
	if err := os.WriteFile(out, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runToFile(t, out, "first@example.com", "one", false)
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v, want *ConfigError", err)
	}
	if data, _ := os.ReadFile(out); string(data) != "keep\n" {
		t.Errorf("existing output changed: %q", data)
	}
}
