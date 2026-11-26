package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
)

// newTestRunner builds a minimal Runner with the given source names. Every
// source that takes a key has one, so source selection is not limited by
// missing keys.
func newTestRunner(sourceNames []string) *Runner {
	r := newUnkeyedTestRunner(sourceNames)
	for _, s := range r.available {
		if s.UsesKey() {
			s.AddApiKeys([]string{"host.example:test-key"})
		}
	}
	return r
}

// newUnkeyedTestRunner is newTestRunner without any API key. The scan type
// defaults to domain, which every source supports.
func newUnkeyedTestRunner(sourceNames []string) *Runner {
	opts := &Options{
		Sources: sourceNames,
		Output:  &bytes.Buffer{},
		Timeout: 5 * time.Second,
		Type:    sources.TypeDomain,
	}
	session, err := sources.NewSession(sources.SessionOptions{Timeout: opts.Timeout, UserAgent: "leaker-test"})
	if err != nil {
		panic(err)
	}
	return &Runner{options: opts, available: NewSources(), providerConfig: "provider-config.yaml",
		session: session, verifier: NewVerifier(false, session)}
}

// captureLogs sends log output to a buffer until the test ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	logger.SetNoColor(true)
	level := logger.GetMaxLevel()
	logger.SetMaxLevel(logger.LevelInfo)
	t.Cleanup(func() {
		logger.SetOutput(os.Stderr)
		logger.SetNoColor(false)
		logger.SetMaxLevel(level)
	})
	return &buf
}

// TestConfigureSources_ExplicitWithoutKeyIsConfigError: a source named in -s
// without a key stops the run before any request, naming the sources.
func TestConfigureSources_ExplicitWithoutKeyIsConfigError(t *testing.T) {
	r := newUnkeyedTestRunner([]string{"snusbase", "intelx"})
	err := r.configureSources()
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v, want *ConfigError", err)
	}
	for _, name := range []string{"snusbase", "intelx"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

// TestConfigureSources_GroupWarnsActiveAndSkipped: groups skip sources
// without keys and say so once.
func TestConfigureSources_GroupWarnsActiveAndSkipped(t *testing.T) {
	logs := captureLogs(t)
	r := newUnkeyedTestRunner([]string{"online"})
	for _, s := range r.available {
		if s.Name() == "leakcheck" || s.Name() == "snusbase" {
			s.AddApiKeys([]string{"k"})
		}
	}
	if err := r.configureSources(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// two keyed sources plus hudsonrock and proxynova, which need no key
	if len(r.scanSources) != 4 {
		t.Errorf("got %d active sources, want 4", len(r.scanSources))
	}
	want := "4 of 15 sources active; 11 skipped (no key)"
	if n := strings.Count(logs.String(), want); n != 1 {
		t.Errorf("want %q once in logs, found %d times:\n%s", want, n, logs)
	}
}

func TestConfigureSources_ZeroRunnableIsConfigError(t *testing.T) {
	r := newUnkeyedTestRunner([]string{"online"})
	r.available = []sources.Source{&fakeSource{name: "needskey", needsKey: true}}
	var cfgErr *ConfigError
	if err := r.configureSources(); !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v, want *ConfigError", err)
	}
}

func TestConfigureSources_InvalidIsConfigError(t *testing.T) {
	r := newTestRunner([]string{"nonexistent-source"})
	var cfgErr *ConfigError
	if err := r.configureSources(); !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v, want *ConfigError", err)
	}
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
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("blank-only input searches nothing: err = %v, want *ConfigError", err)
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
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("no valid target: err = %v, want *ConfigError", err)
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
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("no valid phone: err = %v, want *ConfigError", err)
	}
}

// TestMatchesTargetType_Email checks email syntax validation, including TLDs
// longer than four characters (e.g. ".group", ".photography"), which were
// previously rejected by an over-restrictive {2,4} quantifier.
func TestMatchesTargetType_Email(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"user@example.ru", true},
		{"first.last@example.group", true}, // long TLD, dotted local part
		{"user@example.com", true},
		{"user@sub.example.photography", true}, // long TLD, subdomain
		{"a+b@c.co", true},
		{"notanemail", false},
		{"notanemail.com", false},
		{"missing@tld", false},
		{"", false},
	}
	for _, c := range cases {
		if got := matchesTargetType(sources.TypeEmail, c.line); got != c.want {
			t.Errorf("matchesTargetType(email, %q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestEnumerateMultipleTargets_SkipsNonDomainForDomainType(t *testing.T) {
	r := newTestRunner([]string{})
	r.options.Type = sources.TypeDomain
	r.options.NoFilter = true

	var out bytes.Buffer
	input := strings.NewReader("not a domain\nuser@example.com\n")
	err := r.EnumerateMultipleTargets(context.Background(), input, []io.Writer{&out})
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("no valid domain: err = %v, want *ConfigError", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for non-domain input, got: %q", out.String())
	}
}

// fakeSource is a minimal Source implementation used to test enumerate flow
// without hitting real APIs. Each fake emits the given Results once and
// closes its channel.
type fakeSource struct {
	name     string
	emits    []sources.Result
	onStart  func() // optional hook invoked when Run is called, for ordering assertions
	needsKey bool
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
func (f *fakeSource) Name() string               { return f.name }
func (f *fakeSource) UsesKey() bool              { return false }
func (f *fakeSource) NeedsKey() bool             { return f.needsKey }
func (f *fakeSource) AddApiKeys([]string)        {}
func (f *fakeSource) HasKeys() bool              { return false }
func (f *fakeSource) MinInterval() time.Duration { return 0 }

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
	err = r.EnumerateSingleTarget(context.Background(), "dup@example.com", sources.TypeEmail, []io.Writer{&out})
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
	err = r.EnumerateSingleTarget(context.Background(), "new@example.com", sources.TypeEmail, []io.Writer{&out})
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
	err := r.EnumerateSingleTarget(context.Background(), "x@example.com", sources.TypeEmail, []io.Writer{&out})
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
	err := r.EnumerateSingleTarget(context.Background(), "a@b.com", sources.TypeEmail, []io.Writer{&out})
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
func (o *orderingSource) Name() string               { return o.inner.Name() }
func (o *orderingSource) UsesKey() bool              { return o.inner.UsesKey() }
func (o *orderingSource) NeedsKey() bool             { return o.inner.NeedsKey() }
func (o *orderingSource) AddApiKeys([]string)        {}
func (o *orderingSource) HasKeys() bool              { return o.inner.HasKeys() }
func (o *orderingSource) MinInterval() time.Duration { return o.inner.MinInterval() }

// TestEnumerate_FilterIsTypeAware runs the default result filter on the
// phone and domain scenarios from the v1.8 brief.
func TestEnumerate_FilterIsTypeAware(t *testing.T) {
	cases := []struct {
		name   string
		st     sources.ScanType
		target string
		emits  []sources.Result
		want   []string
	}{
		{
			name: "phone formats", st: sources.TypePhone, target: "15552341096",
			emits: []sources.Result{{Source: "s", Phone: "15552341096", Password: "a"}, {Source: "s", Phone: "+1 (555) 234-1096", Password: "b"}},
			want:  []string{"password:a, phone:15552341096", "password:b, phone:+1 (555) 234-1096"},
		},
		{
			name: "domain look-alikes", st: sources.TypeDomain, target: "example.com",
			emits: []sources.Result{
				{Source: "s", Email: "a@example.com"}, {Source: "s", Email: "x@sub.example.com"},
				{Source: "s", Email: "a@notexample.com"}, {Source: "s", Email: "b@example.com.evil.org"},
			},
			want: []string{"email:a@example.com", "email:x@sub.example.com"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newTestRunner(nil)
			r.scanSources = []sources.Source{&fakeSource{name: "s", emits: c.emits}}
			r.options.Type = c.st

			var out bytes.Buffer
			if err := r.EnumerateSingleTarget(context.Background(), c.target, c.st, []io.Writer{&out}); err != nil {
				t.Fatalf("enumerate: %v", err)
			}
			got := strings.Split(strings.TrimSpace(out.String()), "\n")
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("output lines %q, want %q", got, c.want)
			}
		})
	}
}

// TestConfigureSources_UnsupportedScanType: a domain-only source cannot run
// an email search. Named explicitly it is a config error; in a group it is
// skipped and counted.
func TestConfigureSources_UnsupportedScanType(t *testing.T) {
	r := newTestRunner([]string{"hibr"})
	r.options.Type = sources.TypeEmail
	err := r.configureSources()
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), "hibr") || !strings.Contains(err.Error(), "domain") {
		t.Fatalf("err = %v, want a *ConfigError naming hibr and domain searches", err)
	}

	logs := captureLogs(t)
	r = newTestRunner([]string{"online"})
	r.options.Type = sources.TypeEmail
	if err := r.configureSources(); err != nil {
		t.Fatal(err)
	}
	for _, s := range r.scanSources {
		if s.Name() == "hibr" || s.Name() == "breachhouse" {
			t.Errorf("%s cannot search emails but is active", s.Name())
		}
	}
	if want := "13 of 15 sources active; 2 skipped (no email search)"; !strings.Contains(logs.String(), want) {
		t.Errorf("want %q in logs:\n%s", want, logs)
	}
}

// errorSource only fails.
type errorSource struct{ fakeSource }

func (e *errorSource) Run(context.Context, string, sources.ScanType, *sources.Session) <-chan sources.Result {
	out := make(chan sources.Result, 1)
	out <- sources.Result{Source: e.name, Error: errors.New("proxyconnect tcp: connection refused")}
	close(out)
	return out
}

// TestEnumerateSingleTarget_AllSourcesFailed: when no source could search,
// the run must not claim "0 leaks".
func TestEnumerateSingleTarget_AllSourcesFailed(t *testing.T) {
	logs := captureLogs(t)
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.scanSources = []sources.Source{&errorSource{fakeSource{name: "a"}}, &errorSource{fakeSource{name: "b"}}}
	err := r.EnumerateSingleTarget(context.Background(), "jane@example.com", sources.TypeEmail, []io.Writer{io.Discard})
	if err == nil {
		t.Fatal("expected an error when every source failed")
	}
	if strings.Contains(logs.String(), "Found 0 leaks") {
		t.Errorf("must not report 0 leaks:\n%s", logs)
	}

	logs.Reset()
	r.scanSources = []sources.Source{&errorSource{fakeSource{name: "a"}},
		&fakeSource{name: "b", emits: []sources.Result{{Source: "b", Email: "jane@example.com", Password: "x"}}}}
	if err := r.EnumerateSingleTarget(context.Background(), "jane@example.com", sources.TypeEmail, []io.Writer{io.Discard}); err != nil {
		t.Fatalf("partial failure is not an error: %v", err)
	}
	if !strings.Contains(logs.String(), "Found 1 leaks for jane@example.com") || !strings.Contains(logs.String(), "1 of 2 sources failed") {
		t.Errorf("summary must count the failed source:\n%s", logs)
	}
}

// TestRunEnumeration_AllSourcesFailedIsRuntimeError: the error reaches the
// caller once, as a runtime error (exit code 1).
func TestRunEnumeration_AllSourcesFailedIsRuntimeError(t *testing.T) {
	captureLogs(t)
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.Targets = "-"
	r.options.StdinReader = strings.NewReader("a@example.com\nb@example.com\n")
	r.scanSources = []sources.Source{&errorSource{fakeSource{name: "a"}}}
	err := r.RunEnumeration(context.Background())
	var cfgErr *ConfigError
	if err == nil || errors.As(err, &cfgErr) || !strings.Contains(err.Error(), "2 of 2 targets") {
		t.Fatalf("err = %v, want a runtime error about 2 of 2 targets", err)
	}
}
