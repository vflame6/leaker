package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vflame6/leaker/internal/fakeapi"
	"github.com/vflame6/leaker/runner/sources"
)

// dehashedAPI answers like DeHashed for a query matching total entries,
// honouring page and size, with 95 credits left.
func dehashedAPI(t *testing.T, total int) *fakeapi.Server {
	t.Helper()
	return fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Page, Size int }
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if req.Size < 1 || req.Size > 10000 || req.Page < 1 || req.Page*req.Size > 10000 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var entries []string
		for i := (req.Page - 1) * req.Size; i < total && i < req.Page*req.Size; i++ {
			entries = append(entries, fmt.Sprintf(`{"email":["user%d@example.com"],"password":["pw%d"],"database_name":"DB"}`, i, i))
		}
		_, _ = fmt.Fprintf(w, `{"balance":95,"total":%d,"entries":[%s]}`, total, strings.Join(entries, ","))
	}))
}

// TestPaging_DeHashedAcceptance is the B3 acceptance check: with a total of
// 4,213 the default run fetches 1,000 and prints the truncation line, --all
// fetches 4,213, and --max-requests 1 sends exactly one request.
func TestPaging_DeHashedAcceptance(t *testing.T) {
	cases := []struct {
		name                    string
		maxResults, maxRequests int
		written                 int
		notice                  bool
	}{
		{"defaults", 1000, 10, 1000, true},
		{"--all", 0, 0, 4213, false},
		{"--max-requests 1", 1000, 1, 1000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logs := captureLogs(t)
			captureSummary(t)
			srv := dehashedAPI(t, 4213)
			r := newTestRunner([]string{"dehashed"})
			r.options.Type = sources.TypeDomain
			r.options.MaxResults, r.options.MaxRequests = c.maxResults, c.maxRequests
			r.session = testSession(t, srv.Transport())
			if err := r.configureSources(); err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			if err := r.EnumerateSingleTarget(context.Background(), "example.com", sources.TypeDomain, []io.Writer{&out}); err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(out.String(), "\n"); n != c.written {
				t.Errorf("%d results written, want %d", n, c.written)
			}
			const notice = "dehashed: 1000 of 4213 fetched; raise --max-results or use --all"
			if got := strings.Contains(logs.String(), notice); got != c.notice {
				t.Errorf("notice printed = %v, want %v:\n%s", got, c.notice, logs)
			}
			if n := len(srv.Requests()); n != 1 {
				t.Errorf("%d requests, want exactly 1", n)
			}
		})
	}
}

// TestSearchSource_CapsAtMaxResults: a source that returns everything in one
// answer is cut at --max-results too.
func TestSearchSource_CapsAtMaxResults(t *testing.T) {
	captureLogs(t)
	summary := captureSummary(t)
	var many []sources.Result
	for i := 0; i < 1500; i++ {
		many = append(many, sources.Result{Email: fmt.Sprintf("user%d@example.com", i), Password: "x"})
	}
	r := newTestRunner(nil)
	r.options.Type = sources.TypeDomain
	r.options.Summary = true // --stats
	r.options.MaxResults = 1000
	r.scanSources = []sources.Source{&fakeSource{name: "big", emits: many}}
	var out strings.Builder
	if err := r.EnumerateSingleTarget(context.Background(), "example.com", sources.TypeDomain, []io.Writer{&out}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "\n"); n != 1000 {
		t.Errorf("%d results written, want 1000", n)
	}
	row := summaryRows(t, summary.String())["big"]
	if row.status != StatusTruncated || row.fetched != "1000" {
		t.Errorf("row %+v, want truncated with 1000 fetched", row)
	}
}

// TestTruncationNotice_SourceDefault: IntelX says how many files it read and
// how to read more or all of them.
func TestTruncationNotice_SourceDefault(t *testing.T) {
	stats := sources.Stats{Fetched: 14, Total: -1, Truncated: true, Limit: sources.LimitRequests, Note: "from 10 of 57 files"}
	limits := sources.Limits{DefaultRequests: 10}
	const want = "intelx: 14 fetched from 10 of 57 files; raise --max-requests (default 10 for intelx) or use --all"
	if got := truncationNotice("intelx", stats, limits, true); got != want {
		t.Errorf("notice\n got %q\nwant %q", got, want)
	}
	const typed = "intelx: 14 fetched from 10 of 57 files; raise --max-requests or use --all"
	if got := truncationNotice("intelx", stats, limits, false); got != typed {
		t.Errorf("notice with a typed --max-requests\n got %q\nwant %q", got, typed)
	}
}

func TestTruncationNotice(t *testing.T) {
	cases := []struct {
		stats  sources.Stats
		limits sources.Limits
		want   string
	}{
		{sources.Stats{Fetched: 1000, Total: 4213, Truncated: true, Limit: sources.LimitResults}, sources.Limits{},
			"dehashed: 1000 of 4213 fetched; raise --max-results or use --all"},
		{sources.Stats{Fetched: 300, Total: -1, Truncated: true, Limit: sources.LimitRequests}, sources.Limits{},
			"dehashed: 300 fetched; raise --max-requests or use --all"},
		{sources.Stats{Fetched: 100, Total: 523, Truncated: true, Limit: sources.LimitAPI}, sources.Limits{MaxResults: 100},
			"dehashed: 100 of 523 fetched; the API returns at most 100 per search"},
		{sources.Stats{Fetched: 3000, Total: 5000, Truncated: true, Limit: sources.LimitAPI}, sources.Limits{},
			"dehashed: 3000 of 5000 fetched; the API serves no more"},
		{sources.Stats{Fetched: 100000, Total: -1, Truncated: true, Limit: sources.LimitSafety}, sources.Limits{},
			"dehashed: 100000 fetched; stopped after 1000 requests, give --max-requests to fetch more"},
	}
	for _, c := range cases {
		if got := truncationNotice("dehashed", c.stats, c.limits, false); got != c.want {
			t.Errorf("truncationNotice(%+v)\n got %q\nwant %q", c.stats, got, c.want)
		}
	}
}

// TestQuery_SourceDefaultRequests: a source's own default budget (IntelX's 10
// file reads) applies when --max-requests is not given, never over a typed
// value or --all.
func TestQuery_SourceDefaultRequests(t *testing.T) {
	ten := &fakeSource{name: "ten", limits: sources.Limits{DefaultRequests: 10}}
	plain := &fakeSource{name: "plain"}
	cases := []struct {
		name        string
		src         sources.Source
		maxRequests int
		typed       bool
		want        int
	}{
		{"default budget", ten, 3, false, 10},
		{"typed --max-requests", ten, 3, true, 3},
		{"--all", ten, 0, false, 0},
		{"source without its own default", plain, 3, false, 3},
	}
	for _, c := range cases {
		r := newTestRunner(nil)
		r.options.MaxRequests, r.options.MaxRequestsTyped = c.maxRequests, c.typed
		if got := r.query(c.src, "jane@example.com", sources.TypeEmail).MaxRequests; got != c.want {
			t.Errorf("%s: MaxRequests %d, want %d", c.name, got, c.want)
		}
	}
}
