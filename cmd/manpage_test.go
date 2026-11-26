package cmd

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func manPage(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	date := time.Date(2026, time.November, 26, 12, 0, 0, 0, time.UTC)
	if err := writeManPage(&buf, testParser(t).Model, "v9.9.9", date); err != nil {
		t.Fatalf("writeManPage: %v", err)
	}
	return buf.String()
}

func TestManPage_Header(t *testing.T) {
	page := manPage(t)
	want := `.TH LEAKER 1 2026-11-26 "leaker v9.9.9" "User Commands"`
	if !strings.Contains(page, want+"\n") {
		t.Errorf("page has no %q line:\n%s", want, page)
	}
}

func TestManPage_Sections(t *testing.T) {
	page := manPage(t)
	last := -1
	for _, section := range []string{"NAME", "SYNOPSIS", "DESCRIPTION", "COMMANDS", "OPTIONS",
		"EXIT STATUS", "ENVIRONMENT", "FILES", "EXAMPLES", "SEE ALSO"} {
		i := strings.Index(page, `.SH "`+section+`"`+"\n")
		if i < 0 {
			t.Errorf("no %s section", section)
			continue
		}
		if i < last {
			t.Errorf("%s section is out of order", section)
		}
		last = i
	}
}

// TestManPage_DocumentsTheCLI guards against a flag or command added to the
// CLI without reaching the man page.
func TestManPage_DocumentsTheCLI(t *testing.T) {
	page := manPage(t)
	app := testParser(t).Model
	for _, f := range app.Flags {
		if f.Hidden {
			continue
		}
		if want := `\fB` + roffEscape("--"+f.Name) + `\fR`; !strings.Contains(page, want) {
			t.Errorf("flag --%s is not documented (want %q)", f.Name, want)
		}
	}
	for _, c := range app.Children {
		entry := `\fB` + c.Name + `\fR`
		if c.Hidden && strings.Contains(page, entry) {
			t.Errorf("hidden command %s is documented", c.Name)
		}
		if !c.Hidden && !strings.Contains(page, entry) {
			t.Errorf("command %s is not documented", c.Name)
		}
	}
	for _, code := range []int{ExitOK, ExitError, ExitUsage, ExitInterrupted} {
		if want := ".B " + strconv.Itoa(code) + "\n"; !strings.Contains(page, want) {
			t.Errorf("exit status %d is not documented", code)
		}
	}
	for _, env := range []string{"LEAKER_PROVIDER_CONFIG", "LEAKER_DB", "LEAKER_NO_WRITE_DB", "HTTPS_PROXY"} {
		if !strings.Contains(page, env) {
			t.Errorf("environment variable %s is not documented", env)
		}
	}
}

func TestManPage_MandocLint(t *testing.T) {
	mandoc, err := exec.LookPath("mandoc")
	if err != nil {
		t.Skip("mandoc is not installed")
	}
	out, err := exec.Command(mandoc, "-Tlint", "-Wwarning", writeTemp(t, "leaker.1", manPage(t))).CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Errorf("mandoc -Tlint: %v\n%s", err, out)
	}
}

func TestRoffEscape(t *testing.T) {
	cases := map[string]string{
		"--json":           `\-\-json`,
		`C:\leaker`:        `C:\eleaker`,
		".starts the line": `\&.starts the line`,
		"'starts the line": `\&'starts the line`,
		"plain text":       "plain text",
	}
	for in, want := range cases {
		if got := roffEscape(in); got != want {
			t.Errorf("roffEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
