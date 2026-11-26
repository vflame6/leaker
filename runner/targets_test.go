package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
	"github.com/vflame6/leaker/runner/sources"
)

// untouchableStdin fails the test if anything reads it.
type untouchableStdin struct{ t *testing.T }

func (u untouchableStdin) Read([]byte) (int, error) {
	u.t.Error("stdin was read although a target was given")
	return 0, io.EOF
}

func readTargets(t *testing.T, r *Runner) string {
	t.Helper()
	reader, closeTargets, err := r.openTargets()
	if err != nil {
		t.Fatalf("openTargets: %v", err)
	}
	defer closeTargets()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

func TestOpenTargets_PositionalTargetIgnoresStdin(t *testing.T) {
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.Targets = "jane@example.com"
	r.options.Stdin = true
	r.options.StdinReader = untouchableStdin{t}
	if got := readTargets(t, r); got != "jane@example.com\n" {
		t.Errorf("targets = %q", got)
	}
}

func TestOpenTargets_DashReadsStdin(t *testing.T) {
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.Targets = "-"
	r.options.StdinReader = strings.NewReader("a@example.com\nb@example.com\n")
	if got := readTargets(t, r); got != "a@example.com\nb@example.com\n" {
		t.Errorf("targets = %q", got)
	}
}

func TestOpenTargets_NoPositionalReadsPipedStdin(t *testing.T) {
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.Stdin = true
	r.options.StdinReader = strings.NewReader("piped@example.com\n")
	if got := readTargets(t, r); got != "piped@example.com\n" {
		t.Errorf("targets = %q", got)
	}
}

func TestOpenTargets_ListFlag(t *testing.T) {
	list := filepath.Join(t.TempDir(), "emails")
	if err := os.WriteFile(list, []byte("a@example.com\nb@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.ListFile = list
	r.options.Stdin = true
	r.options.StdinReader = untouchableStdin{t}
	if got := readTargets(t, r); got != "a@example.com\nb@example.com\n" {
		t.Errorf("targets = %q", got)
	}

	r.options.ListFile = filepath.Join(t.TempDir(), "missing.txt")
	var cfgErr *ConfigError
	if _, _, err := r.openTargets(); !errors.As(err, &cfgErr) {
		t.Errorf("missing list: err = %v, want *ConfigError", err)
	}
}

func TestOpenTargets_NoTargetsIsConfigError(t *testing.T) {
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	var cfgErr *ConfigError
	if _, _, err := r.openTargets(); !errors.As(err, &cfgErr) {
		t.Errorf("err = %v, want *ConfigError", err)
	}
}

// TestOpenTargets_ExistingFileIsReadAsList: a positional target that names an
// existing file is read as a list of targets, one per line, without any
// warning, whatever the file is called.
func TestOpenTargets_ExistingFileIsReadAsList(t *testing.T) {
	cases := []struct {
		st   sources.ScanType
		file string
	}{
		{sources.TypeUsername, "admin"},
		{sources.TypeUsername, "usernames.txt"},
		{sources.TypeDomain, "domains.txt"},
		{sources.TypeEmail, "emails"},
		{sources.TypeKeyword, "notes.txt"},
	}
	for _, c := range cases {
		logs := captureLogs(t)
		t.Chdir(t.TempDir())
		if err := os.WriteFile(c.file, []byte("one\ntwo\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := newTestRunner(nil)
		r.options.Type = c.st
		r.options.Targets = c.file
		if got := readTargets(t, r); got != "one\ntwo\n" {
			t.Errorf("%s: targets = %q, want the file's lines", c.file, got)
		}
		if strings.Contains(logs.String(), "[WARN]") {
			t.Errorf("%s: reading a target file must not warn, logs:\n%s", c.file, logs)
		}
	}
}

// TestOpenTargets_MissingFileIsSearchedAsTarget: without such a file the
// positional value is the target itself.
func TestOpenTargets_MissingFileIsSearchedAsTarget(t *testing.T) {
	t.Chdir(t.TempDir())
	r := newTestRunner(nil)
	r.options.Type = sources.TypeUsername
	r.options.Targets = "admin"
	if got := readTargets(t, r); got != "admin\n" {
		t.Errorf("targets = %q, want the literal target", got)
	}
}

// blockingSource blocks until the run is cancelled and counts its runs.
type blockingSource struct {
	runs    atomic.Int32
	started chan struct{}
}

func (b *blockingSource) Search(ctx context.Context, _ sources.Query, _ *sources.Session, _ func(sources.Result)) (sources.Stats, error) {
	if b.runs.Add(1) == 1 {
		close(b.started)
	}
	<-ctx.Done()
	return sources.Stats{Total: -1}, ctx.Err()
}
func (b *blockingSource) Types() []sources.ScanType  { return sources.AllTypes }
func (b *blockingSource) Limits() sources.Limits     { return sources.Limits{} }
func (b *blockingSource) Name() string               { return "blocking" }
func (b *blockingSource) UsesKey() bool              { return false }
func (b *blockingSource) NeedsKey() bool             { return false }
func (b *blockingSource) AddApiKeys([]string)        {}
func (b *blockingSource) HasKeys() bool              { return false }
func (b *blockingSource) MinInterval() time.Duration { return 0 }

// TestEnumerateMultipleTargets_InterruptStopsLoop: an interrupt during the
// first of several targets stops the run with one "Interrupted" line and
// returns the cancellation.
func TestEnumerateMultipleTargets_InterruptStopsLoop(t *testing.T) {
	logs := captureLogs(t)
	src := &blockingSource{started: make(chan struct{})}
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.scanSources = []sources.Source{src}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-src.started
		cancel()
	}()
	input := strings.NewReader("a@example.com\nb@example.com\nc@example.com\n")
	err := r.EnumerateMultipleTargets(ctx, input, []io.Writer{&bytes.Buffer{}})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := strings.Count(logs.String(), "Interrupted"); n != 1 {
		t.Errorf("%d \"Interrupted\" lines, want 1:\n%s", n, logs)
	}
	if n := src.runs.Load(); n != 1 {
		t.Errorf("%d targets started after the interrupt, want 1", n)
	}
}

// TestEnumerateMultipleTargets_InterruptWhileWaitingForInput: a blocked
// stdin does not delay the interrupt.
func TestEnumerateMultipleTargets_InterruptWhileWaitingForInput(t *testing.T) {
	captureLogs(t)
	stdin, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.EnumerateMultipleTargets(ctx, stdin, []io.Writer{&bytes.Buffer{}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context error", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("returned after %v, want under 1s", elapsed)
	}
}

// TestEnumerate_RateLimitWaitStopsOnInterrupt: the pause between two
// requests of a source ends as soon as the run is interrupted.
func TestEnumerate_RateLimitWaitStopsOnInterrupt(t *testing.T) {
	captureLogs(t)
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{}")
	}))
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.session = testSession(t, srv.Transport())
	r.scanSources = []sources.Source{&httpSource{fakeSource: fakeSource{name: "slow"}, interval: time.Hour}}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	err := r.EnumerateMultipleTargets(ctx, strings.NewReader("a@example.com\nb@example.com\n"), []io.Writer{&bytes.Buffer{}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the context error", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %v, want right after the interrupt", elapsed)
	}
	if n := len(srv.Requests()); n != 1 {
		t.Errorf("%d requests, want 1: the second waits an hour", n)
	}
}

// endlessTargets yields valid email targets forever.
type endlessTargets struct{ n int }

func (e *endlessTargets) Read(p []byte) (int, error) {
	e.n++
	return copy(p, fmt.Sprintf("user%d@example.com\n", e.n)), nil
}

// endlessJunk yields lines that are never valid targets.
type endlessJunk struct{}

func (endlessJunk) Read(p []byte) (int, error) { return copy(p, "not-a-target\n"), nil }

// cancelingSource cancels the run on its third search.
type cancelingSource struct {
	fakeSource
	runs   int
	cancel context.CancelFunc
}

func (c *cancelingSource) Search(ctx context.Context, q sources.Query, s *sources.Session, emit func(sources.Result)) (sources.Stats, error) {
	c.runs++
	if c.runs == 3 {
		c.cancel()
	}
	return c.fakeSource.Search(ctx, q, s, emit)
}

// TestEnumerateMultipleTargets_InterruptNeverReportsSuccess: however the
// interrupt races with the input reader, the run reports the interrupt.
func TestEnumerateMultipleTargets_InterruptNeverReportsSuccess(t *testing.T) {
	captureLogs(t)
	for i := 0; i < 300; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		r := newTestRunner(nil)
		r.options.Type = sources.TypeEmail
		r.scanSources = []sources.Source{&cancelingSource{fakeSource: fakeSource{name: "instant"}, cancel: cancel}}
		if err := r.EnumerateMultipleTargets(ctx, &endlessTargets{}, []io.Writer{io.Discard}); !errors.Is(err, context.Canceled) {
			t.Fatalf("valid targets, iteration %d: err = %v, want context.Canceled", i, err)
		}

		ctx, cancel = context.WithCancel(context.Background())
		time.AfterFunc(time.Millisecond, cancel)
		if err := r.EnumerateMultipleTargets(ctx, endlessJunk{}, []io.Writer{io.Discard}); !errors.Is(err, context.Canceled) {
			t.Fatalf("invalid lines, iteration %d: err = %v, want context.Canceled", i, err)
		}
	}
}

// TestEnumerateMultipleTargets_NoValidTargetIsConfigError: input with nothing
// to search is a usage error, not a successful empty run.
func TestEnumerateMultipleTargets_NoValidTargetIsConfigError(t *testing.T) {
	for _, c := range []struct {
		st    sources.ScanType
		input string
	}{
		{sources.TypeEmail, "notanemail\n"},
		{sources.TypeEmail, ""},
		{sources.TypePhone, "12345\n"},
		{sources.TypePhone, "abc\n"},
	} {
		logs := captureLogs(t)
		r := newTestRunner(nil)
		r.options.Type = c.st
		err := r.EnumerateMultipleTargets(context.Background(), strings.NewReader(c.input), []io.Writer{io.Discard})
		var cfgErr *ConfigError
		if !errors.As(err, &cfgErr) {
			t.Errorf("input %q: err = %v, want *ConfigError", c.input, err)
		}
		if line := strings.TrimSpace(c.input); line != "" && !strings.Contains(logs.String(), "skipping: "+line) {
			t.Errorf("input %q: the skipped line must be named, logs:\n%s", c.input, logs)
		}
	}
}
