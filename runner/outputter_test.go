package runner

import (
	"bytes"
	"errors"
	"github.com/vflame6/leaker/runner/sources"
	"strings"
	"testing"
)

func TestWritePlainResult_NotVerbose(t *testing.T) {
	var buf bytes.Buffer
	r := &sources.Result{Source: "leakcheck", Email: "user@example.com", Password: "password123"}
	err := WritePlainResult(&buf, false, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "email:user@example.com") {
		t.Errorf("expected email field, got: %q", got)
	}
	if !strings.Contains(got, "password:password123") {
		t.Errorf("expected password field, got: %q", got)
	}
	if strings.Contains(got, "leakcheck") {
		t.Errorf("source should not appear in non-verbose output, got: %q", got)
	}
}

func TestWritePlainResult_Verbose(t *testing.T) {
	var buf bytes.Buffer
	r := &sources.Result{Source: "proxynova", Email: "user@example.com", Password: "secret"}
	err := WritePlainResult(&buf, true, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "[proxynova] ") {
		t.Errorf("expected verbose line with source prefix, got: %q", got)
	}
}

// errWriter always returns an error on Write.
type errWriter struct{}

func (e *errWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write error")
}

func TestWritePlainResult_PropagatesWriteError(t *testing.T) {
	r := &sources.Result{Source: "src", Email: "test@test.com"}
	err := WritePlainResult(&errWriter{}, false, r)
	if err == nil {
		t.Error("expected error from failing writer, got nil")
	}
}

func TestWriteJSONResult_ValidOutput(t *testing.T) {
	var buf bytes.Buffer
	r := &sources.Result{Source: "leakcheck", Email: "user@example.com", Password: "abc"}
	err := WriteJSONResult(&buf, r, "user@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"source":"leakcheck"`) {
		t.Errorf("expected source field, got: %q", out)
	}
	if !strings.Contains(out, `"target":"user@example.com"`) {
		t.Errorf("expected target field, got: %q", out)
	}
	if !strings.Contains(out, `"email":"user@example.com"`) {
		t.Errorf("expected email field, got: %q", out)
	}
	if !strings.Contains(out, `"password":"abc"`) {
		t.Errorf("expected password field, got: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("expected newline at end, got: %q", out)
	}
}

func TestWriteJSONResult_EscapesSpecialChars(t *testing.T) {
	var buf bytes.Buffer
	r := &sources.Result{Source: "src", Password: `value with "quotes" and \backslash`}
	err := WriteJSONResult(&buf, r, "target")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// result should be valid JSON — parse it to verify
	out := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(out, "{") || !strings.HasSuffix(out, "}") {
		t.Errorf("expected valid JSON object, got: %q", out)
	}
}
