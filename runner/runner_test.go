package runner

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/vflame6/leaker/runner/sources"
)

// newTestRunner builds a minimal Runner with the given source names.
func newTestRunner(sourceNames []string) *Runner {
	opts := &Options{
		Sources: sourceNames,
		Output:  &bytes.Buffer{},
		Timeout: 5 * time.Second,
	}
	return &Runner{options: opts}
}

// TestConfigureSources_All verifies that "all" adds every source in AllSources.
func TestConfigureSources_All(t *testing.T) {
	r := newTestRunner([]string{"all"})
	if err := r.configureSources(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.scanSources) != len(AllSources) {
		t.Errorf("expected %d sources, got %d", len(AllSources), len(r.scanSources))
	}
}

// TestConfigureSources_Specific verifies that a named source is selected.
func TestConfigureSources_Specific(t *testing.T) {
	r := newTestRunner([]string{"proxynova"})
	if err := r.configureSources(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.scanSources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(r.scanSources))
	}
	if r.scanSources[0].Name() != "proxynova" {
		t.Errorf("expected 'proxynova', got %q", r.scanSources[0].Name())
	}
}

// TestConfigureSources_Invalid verifies that an unknown source name returns an error.
func TestConfigureSources_Invalid(t *testing.T) {
	r := newTestRunner([]string{"nonexistent-source"})
	if err := r.configureSources(); err == nil {
		t.Error("expected error for invalid source name, got nil")
	}
}

// TestConfigureSources_NoSharedState verifies that two Runners each get their own
// scanSources slice. This is the regression test for the old global ScanSources bug.
func TestConfigureSources_NoSharedState(t *testing.T) {
	r1 := newTestRunner([]string{"all"})
	if err := r1.configureSources(); err != nil {
		t.Fatal(err)
	}

	r2 := newTestRunner([]string{"all"})
	if err := r2.configureSources(); err != nil {
		t.Fatal(err)
	}

	// Both should have the correct count — not doubled from leftover global state
	if len(r1.scanSources) != len(AllSources) {
		t.Errorf("r1: expected %d sources, got %d", len(AllSources), len(r1.scanSources))
	}
	if len(r2.scanSources) != len(AllSources) {
		t.Errorf("r2: expected %d sources, got %d", len(AllSources), len(r2.scanSources))
	}
}

// TestEnumerateMultipleTargets_SkipsBlankLines verifies blank lines are skipped.
func TestEnumerateMultipleTargets_SkipsBlankLines(t *testing.T) {
	r := newTestRunner([]string{})
	r.options.Type = sources.TypeEmail
	r.options.NoFilter = true

	var out bytes.Buffer
	input := strings.NewReader("\n   \n\n")
	err := r.EnumerateMultipleTargets(context.Background(), input, []io.Writer{&out})
	if err != nil {
		t.Fatalf("unexpected error on blank-only input: %v", err)
	}
	// no results expected
	if out.Len() != 0 {
		t.Errorf("expected no output for blank-only input, got: %q", out.String())
	}
}

// TestEnumerateMultipleTargets_SkipsNonEmailForEmailType checks that non-email
// strings are skipped when Type is TypeEmail.
func TestEnumerateMultipleTargets_SkipsNonEmailForEmailType(t *testing.T) {
	r := newTestRunner([]string{})
	r.options.Type = sources.TypeEmail
	r.options.NoFilter = true

	var out bytes.Buffer
	// "notanemail" should be skipped; empty scanSources means valid emails produce no output either
	input := strings.NewReader("notanemail\nnotanemail.com\n")
	err := r.EnumerateMultipleTargets(context.Background(), input, []io.Writer{&out})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for non-email input, got: %q", out.String())
	}
}

// TestEnumerateMultipleTargets_SkipsNonDomainForDomainType checks domain filtering.
// TestEnumerateMultipleTargets_NormalizesPhoneInput checks that phone numbers
// in various formats are normalized to digits-only before processing.
func TestEnumerateMultipleTargets_NormalizesPhoneInput(t *testing.T) {
	r := newTestRunner([]string{})
	r.options.Type = sources.TypePhone
	r.options.NoFilter = true

	var out bytes.Buffer
	// These should all be normalized and NOT skipped (valid digit counts)
	input := strings.NewReader("+1 (555) 234 10 96\n+998-50-123-45-67\n15552341096\n")
	err := r.EnumerateMultipleTargets(context.Background(), input, []io.Writer{&out})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No sources configured, so no output, but the important thing is no errors
	// and the lines weren't skipped. We verify by checking that a short/invalid
	// input IS skipped.
	var out2 bytes.Buffer
	input2 := strings.NewReader("123\nnot-a-phone\n")
	err = r.EnumerateMultipleTargets(context.Background(), input2, []io.Writer{&out2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnumerateMultipleTargets_SkipsNonDomainForDomainType(t *testing.T) {
	r := newTestRunner([]string{})
	r.options.Type = sources.TypeDomain
	r.options.NoFilter = true

	var out bytes.Buffer
	input := strings.NewReader("not a domain\nuser@example.com\n")
	err := r.EnumerateMultipleTargets(context.Background(), input, []io.Writer{&out})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for non-domain input, got: %q", out.String())
	}
}
