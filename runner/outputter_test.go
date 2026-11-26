package runner

import (
	"bytes"
	"errors"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
	"strings"
	"testing"
)

func TestWritePlainResult_NotVerbose(t *testing.T) {
	var buf bytes.Buffer
	r := &sources.Result{Source: "leakcheck", Email: "user@example.com", Password: "password123"}
	err := WritePlainResult(&buf, false, false, r)
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
	logger.SetNoColor(true)
	defer logger.SetNoColor(false)
	var buf bytes.Buffer
	r := &sources.Result{Source: "proxynova", Email: "user@example.com", Password: "secret"}
	err := WritePlainResult(&buf, true, false, r)
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
	err := WritePlainResult(&errWriter{}, false, false, r)
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

// TestWritePlainResult_TwentyRunsIdentical: the same record always prints the
// same bytes, whatever order its extra fields were set in.
func TestWritePlainResult_TwentyRunsIdentical(t *testing.T) {
	extras := map[string]string{
		"stealer_family": "RedLine", "computer_name": "DESKTOP-1", "operating_system": "Windows 10",
		"date_compromised": "2023-04-01", "ip_geo": "Springfield", "category": "stealer",
	}
	const want = "email:jane@example.com, password:hunter2, category:stealer, computer_name:DESKTOP-1, date_compromised:2023-04-01, " +
		"ip_geo:Springfield, operating_system:Windows 10, stealer_family:RedLine\n"
	for run := 0; run < 20; run++ {
		r := &sources.Result{Email: "jane@example.com", Password: "hunter2"}
		for k, v := range extras { // map order differs from run to run
			r.SetExtra(k, v)
		}
		var buf bytes.Buffer
		if err := WritePlainResult(&buf, false, false, r); err != nil {
			t.Fatal(err)
		}
		if buf.String() != want {
			t.Fatalf("run %d printed\n%q\nwant\n%q", run, buf.String(), want)
		}
	}
}

// TestWriteJSONResult_DatabaseWithoutMetadata: JSON always names the breach;
// -M only changes plain output.
func TestWriteJSONResult_DatabaseWithoutMetadata(t *testing.T) {
	var buf bytes.Buffer
	r := &sources.Result{Source: "snusbase", Email: "jane@example.com", Database: "ExampleDB"}
	if err := WriteJSONResult(&buf, r, "jane@example.com"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"database":"ExampleDB"`) {
		t.Errorf("JSON lacks the database: %s", buf.String())
	}

	buf.Reset()
	if err := WritePlainResult(&buf, false, false, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "ExampleDB") {
		t.Errorf("plain output without -M must not show the database: %s", buf.String())
	}
}
