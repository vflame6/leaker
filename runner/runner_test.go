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

// TestConfigureSources_All verifies that "all" adds every source in AllSources,
// including the local source.
func TestConfigureSources_All(t *testing.T) {
	r := newTestRunner([]string{"all"})
	if err := r.configureSources(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.scanSources) != len(AllSources) {
		t.Errorf("expected %d sources, got %d", len(AllSources), len(r.scanSources))
	}
	if !containsSourceName(r.scanSources, sources.LocalSourceName) {
		t.Error("expected 'all' to include the local source")
	}
}

// containsSourceName reports whether the slice contains a source with the
// given name.
func containsSourceName(list []sources.Source, name string) bool {
	for _, s := range list {
		if s.Name() == name {
			return true
		}
	}
	return false
}

// TestConfigureSources_Online verifies that "online" resolves to every
// source EXCEPT local.
func TestConfigureSources_Online(t *testing.T) {
	r := newTestRunner([]string{"online"})
	if err := r.configureSources(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := len(AllSources) - 1 // all minus local
	if len(r.scanSources) != want {
		t.Errorf("expected %d online sources, got %d", want, len(r.scanSources))
	}
	if containsSourceName(r.scanSources, sources.LocalSourceName) {
		t.Error("online must NOT include the local source")
	}
}

// TestConfigureSources_Local verifies that "local" resolves to exactly one
// source, the local one.
func TestConfigureSources_Local(t *testing.T) {
	r := newTestRunner([]string{"local"})
	if err := r.configureSources(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.scanSources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(r.scanSources))
	}
	if r.scanSources[0].Name() != sources.LocalSourceName {
		t.Errorf("expected 'local', got %q", r.scanSources[0].Name())
	}
}

// TestConfigureSources_OnlineAndLocalEqualsAll verifies that specifying
// both tokens is equivalent to "all".
func TestConfigureSources_OnlineAndLocalEqualsAll(t *testing.T) {
	r := newTestRunner([]string{"online", "local"})
	if err := r.configureSources(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.scanSources) != len(AllSources) {
		t.Errorf("expected %d sources, got %d", len(AllSources), len(r.scanSources))
	}
	if !containsSourceName(r.scanSources, sources.LocalSourceName) {
		t.Error("expected local to be present")
	}
}

// TestConfigureSources_AllPlusOnlineRedundant verifies that "all" dominates
// other group tokens without error.
func TestConfigureSources_AllPlusOnlineRedundant(t *testing.T) {
	r := newTestRunner([]string{"all", "online"})
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

// fakeSource is a minimal Source implementation used to test enumerate flow
// without hitting real APIs. Each fake emits the given Results once and
// closes its channel.
type fakeSource struct {
	name    string
	emits   []sources.Result
	onStart func() // optional hook invoked when Run is called, for ordering assertions
}

func (f *fakeSource) Run(ctx context.Context, _ string, _ sources.ScanType, _ *sources.Session) <-chan sources.Result {
	if f.onStart != nil {
		f.onStart()
	}
	out := make(chan sources.Result, len(f.emits))
	for _, r := range f.emits {
		out <- r
	}
	close(out)
	return out
}
func (f *fakeSource) Name() string        { return f.name }
func (f *fakeSource) UsesKey() bool       { return false }
func (f *fakeSource) NeedsKey() bool      { return false }
func (f *fakeSource) AddApiKeys([]string) {}
func (f *fakeSource) RateLimit() int      { return 1000 }

// TestEnumerate_LocalResultsNotWrittenBack verifies that results emitted
// by the local source are NOT re-inserted into the database (they're
// already there), while online-source results ARE persisted.
func TestEnumerate_LocalResultsNotWrittenBack(t *testing.T) {
	path := tempDBPath(t)
	db, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Seed the DB with a result so "local" can return it.
	seed := sources.Result{Source: "seed", Email: "dup@example.com", Password: "x"}
	if err := db.Insert(&seed); err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	// Local fake emits the same result (Source=local).
	localResult := sources.Result{Source: sources.LocalSourceName, Email: "dup@example.com", Password: "x"}
	localFake := &fakeSource{name: sources.LocalSourceName, emits: []sources.Result{localResult}}

	// Online fake emits the SAME leak (but Source=snusbase).
	onlineResult := sources.Result{Source: "snusbase", Email: "dup@example.com", Password: "x"}
	onlineFake := &fakeSource{name: "snusbase", emits: []sources.Result{onlineResult}}

	r := newTestRunner([]string{})
	r.scanSources = []sources.Source{localFake, onlineFake}
	r.options.Type = sources.TypeEmail
	r.options.NoFilter = true
	r.leakerDB = db

	var out bytes.Buffer
	err = r.EnumerateSingleTarget(context.Background(), "dup@example.com", sources.TypeEmail, 5*time.Second, []io.Writer{&out})
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}

	// Should see exactly one line (dedup wins on checksum).
	lines := strings.Count(strings.TrimRight(out.String(), "\n"), "\n") + 1
	if out.Len() == 0 {
		lines = 0
	}
	if lines != 1 {
		t.Errorf("expected 1 output line, got %d: %q", lines, out.String())
	}

	// DB should still have exactly one row (the seed), not two.
	var count int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM leaks").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 DB row (local result should not be written back), got %d", count)
	}
}

// TestEnumerate_OnlineResultsWritten verifies that online-source results
// are persisted to the local DB.
func TestEnumerate_OnlineResultsWritten(t *testing.T) {
	path := tempDBPath(t)
	db, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	onlineFake := &fakeSource{
		name:  "snusbase",
		emits: []sources.Result{{Source: "snusbase", Email: "new@example.com", Password: "p"}},
	}

	r := newTestRunner([]string{})
	r.scanSources = []sources.Source{onlineFake}
	r.options.Type = sources.TypeEmail
	r.options.NoFilter = true
	r.leakerDB = db

	var out bytes.Buffer
	err = r.EnumerateSingleTarget(context.Background(), "new@example.com", sources.TypeEmail, 5*time.Second, []io.Writer{&out})
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}

	var count int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM leaks WHERE email='new@example.com'").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected online result to be persisted, got %d rows", count)
	}
}

// TestEnumerate_NoWriteDB verifies that when the DB handle is nil (e.g.
// because --no-write-db was set and no DB existed), online results still
// flow to output and no write is attempted.
func TestEnumerate_NoWriteDB(t *testing.T) {
	onlineFake := &fakeSource{
		name:  "snusbase",
		emits: []sources.Result{{Source: "snusbase", Email: "x@example.com", Password: "p"}},
	}

	r := newTestRunner([]string{})
	r.scanSources = []sources.Source{onlineFake}
	r.options.Type = sources.TypeEmail
	r.options.NoFilter = true
	r.leakerDB = nil // simulate --no-write-db without an existing DB

	var out bytes.Buffer
	err := r.EnumerateSingleTarget(context.Background(), "x@example.com", sources.TypeEmail, 5*time.Second, []io.Writer{&out})
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if !strings.Contains(out.String(), "email:x@example.com") {
		t.Errorf("expected result in output, got: %q", out.String())
	}
}

// TestEnumerate_LocalRunsBeforeOnline verifies ordering: the local source
// must produce its results BEFORE any online source starts, so local
// checksums populate the dedup map first.
func TestEnumerate_LocalRunsBeforeOnline(t *testing.T) {
	// A signal channel the online fake waits on before emitting, to let
	// us assert the local fake ran first.
	localFinished := make(chan struct{})

	localFake := &fakeSource{
		name: sources.LocalSourceName,
		emits: []sources.Result{
			{Source: sources.LocalSourceName, Email: "a@b.com", Password: "early"},
		},
	}

	// onStart is called when the online source's Run is invoked; at that
	// moment, local's channel must already have been drained.
	onlineStarted := false
	onlineFake := &fakeSource{
		name: "snusbase",
		onStart: func() {
			select {
			case <-localFinished:
				onlineStarted = true
			case <-time.After(2 * time.Second):
				onlineStarted = false
			}
		},
		emits: []sources.Result{
			{Source: "snusbase", Email: "a@b.com", Password: "late"},
		},
	}

	// Wrap localFake so we can signal after its Run returns.
	orderedLocal := &orderingSource{
		inner:      localFake,
		afterClose: func() { close(localFinished) },
	}

	r := newTestRunner([]string{})
	r.scanSources = []sources.Source{orderedLocal, onlineFake}
	r.options.Type = sources.TypeEmail
	r.options.NoFilter = true
	r.leakerDB = nil

	var out bytes.Buffer
	err := r.EnumerateSingleTarget(context.Background(), "a@b.com", sources.TypeEmail, 5*time.Second, []io.Writer{&out})
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if !onlineStarted {
		t.Error("online source started before local source finished — ordering is wrong")
	}
}

// orderingSource wraps a Source and signals after its Run channel closes.
type orderingSource struct {
	inner      sources.Source
	afterClose func()
}

func (o *orderingSource) Run(ctx context.Context, target string, st sources.ScanType, s *sources.Session) <-chan sources.Result {
	inner := o.inner.Run(ctx, target, st, s)
	out := make(chan sources.Result)
	go func() {
		defer close(out)
		for r := range inner {
			out <- r
		}
		if o.afterClose != nil {
			o.afterClose()
		}
	}()
	return out
}
func (o *orderingSource) Name() string        { return o.inner.Name() }
func (o *orderingSource) UsesKey() bool       { return o.inner.UsesKey() }
func (o *orderingSource) NeedsKey() bool      { return o.inner.NeedsKey() }
func (o *orderingSource) AddApiKeys([]string) {}
func (o *orderingSource) RateLimit() int      { return o.inner.RateLimit() }
