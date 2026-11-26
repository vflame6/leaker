package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
)

func testParser(t *testing.T) *kong.Kong {
	t.Helper()
	parser, err := newParser()
	if err != nil {
		t.Fatalf("newParser: %v", err)
	}
	return parser
}

func completionScript(t *testing.T, shell string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := writeCompletion(&buf, testParser(t).Model, shell); err != nil {
		t.Fatalf("writeCompletion(%s): %v", shell, err)
	}
	return buf.String()
}

// writeTemp writes content to a new file in a temp dir and returns its path.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// lookShell returns the path of a shell a test needs, or skips the test.
func lookShell(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skipf("the %s completion test runs on Unix", name)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not installed", name)
	}
	return path
}

// completionDir returns a working directory holding one file, targets.txt,
// for the file-name completion cases.
func completionDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "targets.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWriteCompletion_UnknownShell(t *testing.T) {
	var buf bytes.Buffer
	if err := writeCompletion(&buf, testParser(t).Model, "powershell"); err == nil {
		t.Fatal("expected an error for an unsupported shell")
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes for an unsupported shell", buf.Len())
	}
}

// TestCompletionScripts_OfferEveryVisibleFlag guards against a flag added to
// the CLI without reaching the scripts.
func TestCompletionScripts_OfferEveryVisibleFlag(t *testing.T) {
	app := testParser(t).Model
	forms := map[string]func(long string, short rune) []string{
		"bash": dashed,
		"zsh":  dashed,
		"fish": func(long string, short rune) []string {
			out := []string{"-l " + long}
			if short != 0 {
				out = append(out, "-s "+string(short))
			}
			return out
		},
	}
	for shell, form := range forms {
		script := completionScript(t, shell)
		for _, f := range app.Flags {
			if f.Hidden {
				continue
			}
			for _, want := range form(f.Name, f.Short) {
				if !strings.Contains(script, want) {
					t.Errorf("%s script does not offer %q", shell, want)
				}
			}
		}
	}
}

func dashed(long string, short rune) []string {
	out := []string{"--" + long}
	if short != 0 {
		out = append(out, "-"+string(short))
	}
	return out
}

// TestNewComplModel_RejectsUnknownCompleteTag: a typo in a complete:"" tag
// fails the tests instead of silently completing nothing.
func TestNewComplModel_RejectsUnknownCompleteTag(t *testing.T) {
	var cli struct {
		Output string `complete:"files"`
	}
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newComplModel(parser.Model, nil); err == nil {
		t.Fatal(`expected an error for complete:"files"`)
	}
}

// bashComplete loads the bash script, completes words in dir (the last word
// is the one under the cursor) with COMP_WORDBREAKS set to breaks, and
// returns the sorted candidates.
func bashComplete(t *testing.T, bash, script, dir, breaks string, words ...string) []string {
	t.Helper()
	var prog strings.Builder
	prog.WriteString("source \"$1\" || exit 1\nCOMP_WORDBREAKS=\"$LEAKER_TEST_WORDBREAKS\"\nCOMP_WORDS=(")
	for _, w := range words {
		prog.WriteString(" '" + strings.ReplaceAll(w, "'", `'\''`) + "'")
	}
	fmt.Fprintf(&prog, " )\nCOMP_CWORD=%d\n", len(words)-1)
	prog.WriteString("_leaker\nfor c in \"${COMPREPLY[@]}\"; do printf '%s\\n' \"$c\"; done\n")

	cmd := exec.Command(bash, "--norc", "--noprofile", "-c", prog.String(), "bash", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LEAKER_TEST_WORDBREAKS="+breaks)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("bash completion of %q: %v\n%s", words, err, stderr.String())
	}
	return sortedLines(string(out))
}

// bashes returns every bash the tests can use: the one on PATH and, on
// macOS, the stock /bin/bash 3.2, which lacks compopt and does not split
// COMP_WORDS at COMP_WORDBREAKS.
func bashes(t *testing.T) []string {
	t.Helper()
	var paths []string
	seen := map[string]bool{}
	candidates := []string{"/bin/bash"}
	if path, err := exec.LookPath("bash"); err == nil {
		candidates = append([]string{path}, candidates...)
	}
	for _, path := range candidates {
		real, err := filepath.EvalSymlinks(path)
		if err != nil || seen[real] {
			continue
		}
		seen[real] = true
		paths = append(paths, path)
	}
	if runtime.GOOS == "windows" || len(paths) == 0 {
		t.Skip("the bash completion tests run on Unix with bash installed")
	}
	return paths
}

func bashVersion(t *testing.T, bash string) string {
	t.Helper()
	out, err := exec.Command(bash, "--norc", "--noprofile", "-c", `echo "$BASH_VERSION"`).Output()
	if err != nil {
		t.Fatalf("%s: %v", bash, err)
	}
	return strings.TrimSpace(string(out))
}

// Word breaks: bash's default, which splits --flag=value, and one without "=".
const (
	breaksWithEquals    = " \t\n\"'@><=;|&(:"
	breaksWithoutEquals = " \t\n\"'@><;|&(:"
)

func sortedLines(s string) []string {
	var lines []string
	for line := range strings.SplitSeq(s, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	slices.Sort(lines)
	return lines
}

func TestBashCompletion(t *testing.T) {
	script := writeTemp(t, "leaker.bash", completionScript(t, "bash"))
	dir := completionDir(t)

	cases := []struct {
		name   string
		breaks string
		words  []string
		want   []string
	}{
		{"commands", breaksWithEquals, []string{"leaker", ""}, []string{"completion", "domain", "email", "keyword", "phone", "sources", "username"}},
		{"command prefix", breaksWithEquals, []string{"leaker", "e"}, []string{"email"}},
		{"hidden command", breaksWithEquals, []string{"leaker", "ma"}, nil},
		{"long flag", breaksWithEquals, []string{"leaker", "--js"}, []string{"--json"}},
		{"flag after the command", breaksWithEquals, []string{"leaker", "email", "--no-w"}, []string{"--no-write-db"}},
		{"source", breaksWithEquals, []string{"leaker", "-s", "snus"}, []string{"snusbase"}},
		{"source group", breaksWithEquals, []string{"leaker", "--sources", "onl"}, []string{"online"}},
		{"second source", breaksWithEquals, []string{"leaker", "-s", "proxynova,snus"}, []string{"proxynova,snusbase"}},
		{"source after =", breaksWithEquals, []string{"leaker", "--sources", "=", "snus"}, []string{"snusbase"}},
		{"file flag", breaksWithEquals, []string{"leaker", "-l", "tar"}, []string{"targets.txt"}},
		{"file flag after =", breaksWithEquals, []string{"leaker", "--list", "=", "tar"}, []string{"targets.txt"}},
		{"file flag at =", breaksWithEquals, []string{"leaker", "--list", "="}, []string{"targets.txt"}},
		// bash 3.2 keeps --flag=value in one word, but readline still
		// replaces only the text after "=".
		{"file flag in one word, = breaks words", breaksWithEquals, []string{"leaker", "--output=tar"}, []string{"targets.txt"}},
		{"source in one word, = breaks words", breaksWithEquals, []string{"leaker", "--sources=onl"}, []string{"online"}},
		{"file flag in one word, = does not break words", breaksWithoutEquals, []string{"leaker", "--output=tar"}, []string{"--output=targets.txt"}},
		{"value for a flag that takes none", breaksWithEquals, []string{"leaker", "--verbose=e"}, nil},
		{"target file", breaksWithEquals, []string{"leaker", "email", "tar"}, []string{"targets.txt"}},
		{"flag value is not a command", breaksWithEquals, []string{"leaker", "-o", "email", "e"}, []string{"email"}},
		{"free-form value", breaksWithEquals, []string{"leaker", "--timeout", ""}, nil},
		{"shells", breaksWithEquals, []string{"leaker", "completion", ""}, []string{"bash", "fish", "zsh"}},
		{"shell prefix", breaksWithEquals, []string{"leaker", "completion", "z"}, []string{"zsh"}},
	}
	for _, bash := range bashes(t) {
		t.Run(bashVersion(t, bash), func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if got := bashComplete(t, bash, script, dir, tc.breaks, tc.words...); !slices.Equal(got, tc.want) {
						t.Errorf("completing %q: got %q, want %q", tc.words, got, tc.want)
					}
				})
			}
		})
	}
}

// TestBashCompletion_QuotesFileNames: without compopt (bash 3.2) the script
// registers with -o filenames, so readline escapes "my file.txt" and adds
// "/" to directories; with compopt it turns that on only for file names.
func TestBashCompletion_QuotesFileNames(t *testing.T) {
	script := writeTemp(t, "leaker.bash", completionScript(t, "bash"))
	for _, bash := range bashes(t) {
		out, err := exec.Command(bash, "--norc", "--noprofile", "-c",
			`source "$1" && complete -p leaker && type compopt >/dev/null 2>&1 && echo has-compopt`, "bash", script).Output()
		if err != nil && !strings.Contains(string(out), "complete ") {
			t.Fatalf("%s: %v\n%s", bash, err, out)
		}
		registration, hasCompopt := string(out), strings.Contains(string(out), "has-compopt")
		if quoted := strings.Contains(registration, "-o filenames"); quoted == hasCompopt {
			t.Errorf("bash %s (compopt: %v) registered %q", bashVersion(t, bash), hasCompopt, strings.TrimSpace(registration))
		}
	}
}

func TestZshCompletion_Syntax(t *testing.T) {
	zsh := lookShell(t, "zsh")
	script := completionScript(t, "zsh")
	if !strings.HasPrefix(script, "#compdef leaker\n") {
		t.Errorf("compinit loads the file only if its first line is #compdef leaker, got %q", strings.SplitN(script, "\n", 2)[0])
	}
	if strings.Contains(script, "'man:") {
		t.Error("the zsh script offers the hidden man command")
	}
	out, err := exec.Command(zsh, "-n", writeTemp(t, "_leaker", script)).CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Errorf("zsh -n: %v\n%s", err, out)
	}
}

// TestZshCompletion completes command lines in a real interactive zsh, through
// testdata/zsh/capture.zsh.
func TestZshCompletion(t *testing.T) {
	zsh := lookShell(t, "zsh")
	fpath := t.TempDir()
	if err := os.WriteFile(filepath.Join(fpath, "_leaker"), []byte(completionScript(t, "zsh")), 0o600); err != nil {
		t.Fatal(err)
	}
	capture, err := filepath.Abs(filepath.Join("testdata", "zsh", "capture.zsh"))
	if err != nil {
		t.Fatal(err)
	}
	init := filepath.Join(filepath.Dir(capture), "init.zsh")
	dir := completionDir(t)

	complete := func(line string) []string {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, zsh, "-f", capture, init, line)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "LEAKER_ZSH_FPATH="+fpath)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("zsh completion of %q: %v\n%s", line, err, stderr.String())
		}
		return sortedLines(string(out))
	}

	cases := map[string][]string{
		"leaker ":                  {"completion", "domain", "email", "keyword", "phone", "sources", "username"},
		"leaker ma":                nil,
		"leaker --js":              {"--json"},
		"leaker email --no-w":      {"--no-write-db"},
		"leaker -s snus":           {"snusbase"},
		"leaker -s proxynova,snus": {"proxynova,snusbase"},
		"leaker --list=tar":        {"--list=targets.txt"},
		"leaker -o tar":            {"targets.txt"},
		"leaker email tar":         {"targets.txt"},
		"leaker -o email e":        {"email"},
		"leaker completion ":       {"bash", "fish", "zsh"},
		"leaker --timeout ":        nil,
	}
	for line, want := range cases {
		if got := complete(line); !slices.Equal(got, want) {
			t.Errorf("completing %q: got %q, want %q", line, got, want)
		}
	}
}

func TestFishCompletion(t *testing.T) {
	fish := lookShell(t, "fish")
	script := writeTemp(t, "leaker.fish", completionScript(t, "fish"))
	dir := completionDir(t)

	complete := func(line string) []string {
		cmd := exec.Command(fish, "--no-config", "-c", "source $argv[1]; and complete --do-complete=$argv[2]", script, line)
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil || stderr.Len() > 0 {
			t.Fatalf("fish completion of %q: %v\n%s", line, err, stderr.String())
		}
		var names []string
		for _, line := range sortedLines(string(out)) {
			name, _, _ := strings.Cut(line, "\t") // drop the description
			names = append(names, name)
		}
		return names
	}

	cases := map[string][]string{
		// fish falls back to substring matches ("ma" finds domain and
		// email), so this case is what shows the hidden man is not offered.
		"leaker ":                       {"completion", "domain", "email", "keyword", "phone", "sources", "username"},
		"leaker --js":                   {"--json"},
		"leaker email --no-w":           {"--no-write-db"},
		"leaker -s snus":                {"snusbase"},
		"leaker -s proxynova,snus":      {"proxynova,snusbase"},
		"leaker --list tar":             {"targets.txt"},
		"leaker email tar":              {"targets.txt"},
		"leaker completion ":            {"bash", "fish", "zsh"},
		"leaker --timeout ":             nil,
		"leaker --provider-config=targ": {"--provider-config=targets.txt"},
		// A flag value that looks like a command is not the command.
		"leaker -o email ":          {"completion", "domain", "email", "keyword", "phone", "sources", "username"},
		"leaker --list completion ": {"completion", "domain", "email", "keyword", "phone", "sources", "username"},
		"leaker -o email e":         {"email"},
		"leaker -o email email tar": {"targets.txt"},
		"leaker --output=email ":    {"completion", "domain", "email", "keyword", "phone", "sources", "username"},
	}
	for line, want := range cases {
		if got := complete(line); !slices.Equal(got, want) {
			t.Errorf("completing %q: got %q, want %q", line, got, want)
		}
	}
}
