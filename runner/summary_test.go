package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/vflame6/leaker/internal/fakeapi"
	"github.com/vflame6/leaker/runner/sources"
)

// captureSummary sends the per-target tables to a buffer until the test ends.
func captureSummary(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := defaultSummaryOutput
	defaultSummaryOutput = &buf
	t.Cleanup(func() { defaultSummaryOutput = previous })
	return &buf
}

// summaryRow is one row of a printed table.
type summaryRow struct {
	status, fetched, kept, requests, credits, time string
}

var columnGap = regexp.MustCompile(`\s{2,}`)

// summaryRows parses the printed tables by source name; a later table wins.
func summaryRows(t *testing.T, text string) map[string]summaryRow {
	t.Helper()
	rows := make(map[string]summaryRow)
	for _, line := range strings.Split(text, "\n") {
		cells := columnGap.Split(strings.TrimSpace(line), -1)
		if len(cells) != 7 || cells[0] == "SOURCE" {
			continue
		}
		rows[cells[0]] = summaryRow{cells[1], cells[2], cells[3], cells[4], cells[5], cells[6]}
	}
	if len(rows) == 0 {
		t.Fatalf("no summary table in:\n%s", text)
	}
	return rows
}

// TestSummary_DeHashedTruncated is the B2 acceptance check: the DeHashed mock
// shows truncated, 100 of 4213, 95 credits left.
func TestSummary_DeHashedTruncated(t *testing.T) {
	captureLogs(t)
	summary := captureSummary(t)
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var entries []string
		for i := 0; i < 100; i++ {
			entries = append(entries, fmt.Sprintf(`{"email":["user%d@example.com"],"password":["pw%d"],"database_name":"DB"}`, i, i))
		}
		_, _ = fmt.Fprintf(w, `{"balance":95,"took":"9ms","total":4213,"entries":[%s]}`, strings.Join(entries, ","))
	}))
	r := newTestRunner([]string{"dehashed"})
	r.options.Type = sources.TypeDomain
	r.options.Summary = true // --stats
	r.options.MaxResults = 100
	r.session = testSession(t, srv.Transport())
	if err := r.configureSources(); err != nil {
		t.Fatal(err)
	}
	if err := r.EnumerateSingleTarget(context.Background(), "example.com", sources.TypeDomain, []io.Writer{io.Discard}); err != nil {
		t.Fatal(err)
	}
	row := summaryRows(t, summary.String())["dehashed"]
	want := summaryRow{status: "truncated", fetched: "100 of 4213", kept: "100", requests: "1", credits: "95 left"}
	row.time = ""
	if row != want {
		t.Errorf("dehashed row %+v, want %+v\n%s", row, want, summary)
	}
}

// TestSummary_Statuses: every source of the selection gets a row, with the
// status of what happened to it.
func TestSummary_Statuses(t *testing.T) {
	captureLogs(t)
	summary := captureSummary(t)
	jane := []sources.Result{{Email: "jane@example.com", Password: "x"}}
	r := newTestRunner([]string{"all"})
	r.available = []sources.Source{
		&fakeSource{name: "a-ok", emits: jane},
		&fakeSource{name: "b-auth", err: fmt.Errorf("status 401: %w", sources.ErrAuth)},
		&fakeSource{name: "c-quota", err: fmt.Errorf("status 402: %w", sources.ErrQuota)},
		&fakeSource{name: "d-rate", err: fmt.Errorf("status 429: %w", sources.ErrRateLimited)},
		&fakeSource{name: "e-error", err: errors.New("boom")},
		&fakeSource{name: "f-truncated", emits: jane, stats: &sources.Stats{Total: 9, Truncated: true, Limit: sources.LimitResults}},
		&fakeSource{name: "g-nokey", needsKey: true},
		&fakeSource{name: "h-unsupported", types: []sources.ScanType{sources.TypeDomain}},
		&fakeSource{name: sources.LocalSourceName},
	}
	r.options.Type = sources.TypeEmail
	r.options.Summary = true // --stats
	if err := r.configureSources(); err != nil {
		t.Fatal(err)
	}
	if err := r.EnumerateSingleTarget(context.Background(), "jane@example.com", sources.TypeEmail, []io.Writer{io.Discard}); err != nil {
		t.Fatal(err)
	}
	rows := summaryRows(t, summary.String())
	want := map[string]string{
		"a-ok": "ok", "b-auth": "auth", "c-quota": "quota", "d-rate": "rate-limited", "e-error": "error",
		"f-truncated": "truncated", "g-nokey": "no key", "h-unsupported": "unsupported", "local": "cached",
	}
	for name, status := range want {
		if got := rows[name].status; got != status {
			t.Errorf("%s: status %q, want %q", name, got, status)
		}
	}
	if got := rows["g-nokey"]; got.fetched != "-" || got.requests != "-" || got.time != "-" {
		t.Errorf("a skipped source has no numbers: %+v", got)
	}
	if got := rows["f-truncated"].fetched; got != "1 of 9" {
		t.Errorf("fetched %q, want \"1 of 9\"", got)
	}
	if len(rows) != len(want) {
		t.Errorf("%d rows, want %d:\n%s", len(rows), len(want), summary)
	}
}

func TestSummary_CreditsCell(t *testing.T) {
	five, twelve := 5, 12
	cases := map[string]SourceSummary{
		"-":               {},
		"5 left":          {CreditsLeft: &five},
		"12 used":         {CreditsUsed: &twelve},
		"12 used, 5 left": {CreditsLeft: &five, CreditsUsed: &twelve},
		"0 left":          {CreditsLeft: new(int)},
	}
	for want, s := range cases {
		if got := creditsCell(s); got != want {
			t.Errorf("creditsCell(%+v) = %q, want %q", s, got, want)
		}
	}
}

// TestSummary_QuietPrintsNoTable: -q prints results only.
func TestSummary_QuietPrintsNoTable(t *testing.T) {
	captureLogs(t)
	summary := captureSummary(t)
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.Summary = true // --stats
	r.options.Quiet = true
	r.scanSources = []sources.Source{&fakeSource{name: "s"}}
	if err := r.EnumerateSingleTarget(context.Background(), "jane@example.com", sources.TypeEmail, []io.Writer{io.Discard}); err != nil {
		t.Fatal(err)
	}
	if summary.Len() != 0 {
		t.Errorf("-q printed a table:\n%s", summary)
	}
}

// TestEnumerate_SourceErrorNamesSourceAndTarget: an error says which source
// failed for which target.
func TestEnumerate_SourceErrorNamesSourceAndTarget(t *testing.T) {
	logs := captureLogs(t)
	captureSummary(t)
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.scanSources = []sources.Source{&errorSource{fakeSource{name: "a"}},
		&fakeSource{name: "b", emits: []sources.Result{{Email: "jane@example.com", Password: "x"}}}}
	if err := r.EnumerateSingleTarget(context.Background(), "jane@example.com", sources.TypeEmail, []io.Writer{io.Discard}); err != nil {
		t.Fatal(err)
	}
	if want := "a failed for jane@example.com: proxyconnect tcp: connection refused"; !strings.Contains(logs.String(), want) {
		t.Errorf("want %q in logs:\n%s", want, logs)
	}
}

// TestStatsFile_WritesJSON: --stats-file saves every target's table as JSON,
// readable by the user only.
func TestStatsFile_WritesJSON(t *testing.T) {
	captureLogs(t)
	captureSummary(t)
	path := filepath.Join(t.TempDir(), "run.json")
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.Version = "v9.9.9"
	r.options.Targets = "-"
	r.options.StdinReader = strings.NewReader("a@example.com\nb@example.com\n")
	r.options.StatsFile = path
	r.options.Output = io.Discard
	r.scanSources = []sources.Source{&fakeSource{name: "s", emits: []sources.Result{{Source: "s", Email: "a@example.com", Password: "x"}}}}
	if err := r.RunEnumeration(context.Background()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var run RunSummary
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, data)
	}
	if run.Version != "v9.9.9" || run.Type != "email" || len(run.Targets) != 2 {
		t.Fatalf("run %+v, want version, type and two targets", run)
	}
	first := run.Targets[0]
	if first.Target != "a@example.com" || first.Found != 1 || len(first.Sources) != 1 {
		t.Fatalf("first target %+v", first)
	}
	if s := first.Sources[0]; s.Source != "s" || s.Status != StatusOK || s.Fetched != 1 || s.Kept != 1 || s.Total != nil {
		t.Errorf("first source %+v", s)
	}
	if run.Targets[1].Found != 0 || run.Targets[1].Sources[0].Kept != 0 {
		t.Errorf("the filter keeps nothing for b@example.com: %+v", run.Targets[1])
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("stats file mode %04o, want 0600", perm)
		}
	}
}

// TestStatsFile_RefusesExistingFile: like -o, an existing file is kept unless
// --overwrite is given.
func TestStatsFile_RefusesExistingFile(t *testing.T) {
	captureLogs(t)
	path := filepath.Join(t.TempDir(), "run.json")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.Targets = "a@example.com"
	r.options.StatsFile = path
	r.scanSources = []sources.Source{&fakeSource{name: "s"}}
	err := r.RunEnumeration(context.Background())
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v, want a *ConfigError", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "keep" {
		t.Errorf("the existing file was changed: %q", data)
	}
}

// TestConfigureSources_UnsupportedNamesTypes: a source named with -s that
// cannot search the type says which types it can search.
func TestConfigureSources_UnsupportedNamesTypes(t *testing.T) {
	r := newTestRunner([]string{"hudsonrock"})
	r.options.Type = sources.TypeKeyword
	err := r.configureSources()
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v, want a *ConfigError", err)
	}
	if want := "hudsonrock cannot run keyword searches (it searches email, username, domain)"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q, want it to contain %q", err, want)
	}
}

// countingWriter counts Write calls.
type countingWriter struct{ writes int }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes++
	return len(p), nil
}

// TestWriteSummaryTable_OneWrite: workers log while the collector prints a
// table, so the table goes out in one write and no log line lands inside.
func TestWriteSummaryTable_OneWrite(t *testing.T) {
	var w countingWriter
	table := TargetSummary{Target: "jane@example.com", Sources: []SourceSummary{
		{Source: "dehashed", Status: StatusOK, Fetched: 3, Kept: 2, Requests: 1},
		{Source: "snusbase", Status: StatusNoKey},
	}}
	if err := writeSummaryTable(&w, table); err != nil {
		t.Fatal(err)
	}
	if w.writes != 1 {
		t.Errorf("%d writes, want 1", w.writes)
	}
}

// TestSummary_PrintedOnlyWhenAsked: the per-target table is printed with
// --stats only.
func TestSummary_PrintedOnlyWhenAsked(t *testing.T) {
	captureLogs(t)
	for _, asked := range []bool{false, true} {
		summary := captureSummary(t)
		r := newTestRunner(nil)
		r.options.Type = sources.TypeEmail
		r.options.Summary = asked
		r.scanSources = []sources.Source{&fakeSource{name: "s"}}
		if err := r.EnumerateMultipleTargets(context.Background(), strings.NewReader("jane@example.com\n"), []io.Writer{io.Discard}); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(summary.String(), "SOURCE"); got != asked {
			t.Errorf("--stats %v: table printed = %v:\n%s", asked, got, summary)
		}
	}
}
