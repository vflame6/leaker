package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
)

// The run's defaults: --max-results 1000, --max-requests 10.
var defaultQuery = Query{MaxResults: 1000, MaxRequests: 10}

func query(q Query, target string, st ScanType) Query {
	q.Target, q.Type = target, st
	return q
}

func checkStats(t *testing.T, stats Stats, total int, limit Limit) {
	t.Helper()
	if stats.Total != total {
		t.Errorf("Total = %d, want %d", stats.Total, total)
	}
	if stats.Truncated != (limit != LimitNone) || stats.Limit != limit {
		t.Errorf("Truncated = %v, Limit = %v, want limit %v", stats.Truncated, stats.Limit, limit)
	}
}

// TestDeHashed_Paging is the B3 acceptance check at the source: with a total
// of 4,213 the defaults fetch 1,000, no limit fetches 4,213, each in one
// request, the page size raised as far as needed.
func TestDeHashed_Paging(t *testing.T) {
	cases := []struct {
		name     string
		q        Query
		results  int
		size     int
		wantLim  Limit
		requests int
	}{
		{"defaults", defaultQuery, 1000, 1000, LimitResults, 1},
		{"--all", Query{}, 4213, 10000, LimitNone, 1},
		{"--max-requests 1", Query{MaxResults: 1000, MaxRequests: 1}, 1000, 1000, LimitResults, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := dehashedMock(t, 4213, 95)
			results, stats, err := searchSource(t, withKeys(&DeHashed{}, testKey), query(c.q, "example.com", TypeDomain), srv.Transport())
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != c.results {
				t.Errorf("%d results, want %d", len(results), c.results)
			}
			checkStats(t, stats, 4213, c.wantLim)
			requests := srv.Requests()
			if len(requests) != c.requests {
				t.Fatalf("%d requests, want %d", len(requests), c.requests)
			}
			var body dehashedSearchRequest
			_ = json.Unmarshal(requests[0].Body, &body)
			if body.Size != c.size || body.Page != 1 {
				t.Errorf("asked for page %d of size %d, want page 1 of size %d", body.Page, body.Size, c.size)
			}
		})
	}
}

// leakRadarMock serves total unlocked leaks for /search/email, page by page,
// and consumes two points per page.
func leakRadarMock(t *testing.T, total int) *fakeapi.Server {
	t.Helper()
	return fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if page < 1 || size < 1 || size > 100 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var items []string
		for i := (page - 1) * size; i < total && i < page*size; i++ {
			items = append(items, fmt.Sprintf(`{"id":"lr-%d","username":"user%d@example.com","password":"pw","unlocked":true}`, i, i))
		}
		_, _ = fmt.Fprintf(w, `{"items":[%s],"total":%d,"page":%d,"page_size":%d,"auto_unlock_points_consumed":2}`,
			strings.Join(items, ","), total, page, size)
	}))
}

func TestLeakRadar_Paging(t *testing.T) {
	srv := leakRadarMock(t, 250)
	results, stats, err := searchSource(t, withKeys(&LeakRadar{}, testKey), query(defaultQuery, "example.com", TypeEmail), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 250 || len(srv.Requests()) != 3 {
		t.Errorf("%d results from %d requests, want 250 from 3", len(results), len(srv.Requests()))
	}
	checkStats(t, stats, 250, LimitNone)
	if stats.CreditsUsed == nil || *stats.CreditsUsed != 6 {
		t.Errorf("CreditsUsed = %v, want the 6 points of 3 pages", stats.CreditsUsed)
	}

	srv = leakRadarMock(t, 250)
	results, stats, err = searchSource(t, withKeys(&LeakRadar{}, testKey), query(Query{MaxResults: 1000, MaxRequests: 2}, "example.com", TypeEmail), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 200 || len(srv.Requests()) != 2 {
		t.Errorf("%d results from %d requests, want 200 from 2", len(results), len(srv.Requests()))
	}
	checkStats(t, stats, 250, LimitRequests)
}

// TestHIBR_PagesCappedByMaxRequests: HIBR pages while has_next is true, up to
// --max-requests pages.
func TestHIBR_PagesCappedByMaxRequests(t *testing.T) {
	var lastPage int
	handler := func(last int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page := 1
			if i := strings.LastIndex(r.URL.Path, "/"); i > 0 {
				if n, err := strconv.Atoi(r.URL.Path[i+1:]); err == nil {
					page = n
				}
			}
			lastPage = page
			_, _ = fmt.Fprintf(w, `{"sources":[{"id_source":"p%d","post_title":"Example %d","website":"example.com"}],"has_next":%v}`, page, page, page < last)
		})
	}
	srv := fakeapi.NewServer(t, handler(10))
	results, stats, err := searchSource(t, withKeys(&HIBR{}, testKey), query(Query{MaxResults: 1000, MaxRequests: 2}, "example.com", TypeDomain), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(srv.Requests()) != 2 || len(results) != 2 || lastPage != 2 {
		t.Errorf("%d requests, %d results, last page %d; want 2, 2, 2", len(srv.Requests()), len(results), lastPage)
	}
	checkStats(t, stats, -1, LimitRequests)

	srv = fakeapi.NewServer(t, handler(3))
	_, stats, err = searchSource(t, withKeys(&HIBR{}, testKey), query(Query{}, "example.com", TypeDomain), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(srv.Requests()) != 3 {
		t.Errorf("%d requests, want the 3 pages", len(srv.Requests()))
	}
	checkStats(t, stats, -1, LimitNone)
}

// whiteIntelMock serves total results, limit per page, and the calls left.
func whiteIntelMock(t *testing.T, total int) *fakeapi.Server {
	t.Helper()
	return fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req whiteIntelRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil || req.Limit < 1 || req.Limit > 5000 || req.Page < 1 {
			_, _ = io.WriteString(w, `{"success":false,"message":"invalid limit or page"}`)
			return
		}
		var results []string
		for i := (req.Page - 1) * req.Limit; i < total && i < req.Page*req.Limit; i++ {
			results = append(results, fmt.Sprintf(`{"url":"https://login.example.com","username":"user%d@example.com","password":"pw"}`, i))
		}
		_, _ = fmt.Fprintf(w, `{"success":true,"remaining_daily_calls":42,"results":[%s]}`, strings.Join(results, ","))
	}))
}

func TestWhiteIntel_PagesWhileFull(t *testing.T) {
	srv := whiteIntelMock(t, 7000)
	results, stats, err := searchSource(t, withKeys(&WhiteIntel{}, testKey), query(Query{}, "example.com", TypeDomain), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 7000 || len(srv.Requests()) != 2 {
		t.Errorf("%d results from %d requests, want 7000 from 2 (a short page is the last)", len(results), len(srv.Requests()))
	}
	checkStats(t, stats, -1, LimitNone)
	if stats.CreditsLeft == nil || *stats.CreditsLeft != 42 {
		t.Errorf("CreditsLeft = %v, want remaining_daily_calls 42", stats.CreditsLeft)
	}

	srv = whiteIntelMock(t, 7000)
	results, stats, err = searchSource(t, withKeys(&WhiteIntel{}, testKey), query(defaultQuery, "example.com", TypeDomain), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1000 || len(srv.Requests()) != 1 {
		t.Errorf("%d results from %d requests, want 1000 from 1", len(results), len(srv.Requests()))
	}
	checkStats(t, stats, -1, LimitResults)
}

func TestWhiteIntel_DailyLimitIsQuota(t *testing.T) {
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"success":false,"message":"Daily limit is reached."}`)
	}))
	_, _, err := searchSource(t, withKeys(&WhiteIntel{}, testKey), query(defaultQuery, "example.com", TypeDomain), srv.Transport())
	if !errors.Is(err, ErrQuota) || errors.Is(err, ErrAuth) {
		t.Errorf("err = %v, want quota", err)
	}
}

func TestOSINTLeak_PagesToTotalPage(t *testing.T) {
	const total = 250
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		var results []string
		for i := (page - 1) * size; i < total && i < page*size; i++ {
			results = append(results, fmt.Sprintf(`{"email":"user%d@example.com","password":"pw"}`, i))
		}
		pages := (total + size - 1) / size
		_, _ = fmt.Fprintf(w, `{"status":"success","results":[%s],"count":%d,"page":%d,"total_page":%d}`, strings.Join(results, ","), total, page, pages)
	}))
	results, stats, err := searchSource(t, withKeys(&OSINTLeak{}, testKey), query(defaultQuery, "example.com", TypeDomain), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != total || len(srv.Requests()) != 3 {
		t.Errorf("%d results from %d requests, want 250 from 3", len(results), len(srv.Requests()))
	}
	checkStats(t, stats, total, LimitNone)
}

// intelxMock serves one search whose result lists files st-1..st-n, each
// holding one line for jane@example.com.
func intelxMock(t *testing.T, files int) *fakeapi.Server {
	t.Helper()
	return fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/intelligent/search":
			_, _ = io.WriteString(w, `{"id":"search-1","status":0}`)
		case "/intelligent/search/result":
			var records []string
			for i := 1; i <= files; i++ {
				records = append(records, fmt.Sprintf(`{"storageid":"st-%d","name":"f%d","bucket":"leaks.public","type":0}`, i, i))
			}
			_, _ = fmt.Fprintf(w, `{"records":[%s],"status":1}`, strings.Join(records, ","))
		case "/intelligent/search/terminate":
			_, _ = io.WriteString(w, "{}")
		case "/file/read":
			_, _ = fmt.Fprintf(w, "jane@example.com:pw-%s\n", r.URL.Query().Get("storageid"))
		default:
			http.NotFound(w, r)
		}
	}))
}

func countPath(requests []fakeapi.Request, path string) int {
	n := 0
	for _, r := range requests {
		if r.URL.Path == path {
			n++
		}
	}
	return n
}

// TestIntelX_FileReadsCappedByMaxRequests: every file read costs a read
// credit, so --max-requests caps them.
func TestIntelX_FileReadsCappedByMaxRequests(t *testing.T) {
	srv := intelxMock(t, 5)
	src := withKeys(&IntelX{pollDelay: time.Millisecond}, "free.intelx.io:"+testKey)
	results, stats, err := searchSource(t, src, query(Query{MaxResults: 1000, MaxRequests: 2}, "jane@example.com", TypeEmail), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	requests := srv.Requests()
	if n := countPath(requests, "/file/read"); n != 2 || len(results) != 2 {
		t.Errorf("%d file reads, %d results; want 2 and 2", n, len(results))
	}
	checkStats(t, stats, -1, LimitRequests)
	if stats.Note != "from 2 of 5 files" {
		t.Errorf("Note = %q, want how many of the files were read", stats.Note)
	}
	var search intelxSearchRequest
	_ = json.Unmarshal(requests[0].Body, &search)
	if search.MaxResults != 1000 {
		t.Errorf("searched for %d records, want min(--max-results, 1000)", search.MaxResults)
	}

	srv = intelxMock(t, 5)
	results, stats, err = searchSource(t, src, query(Query{}, "jane@example.com", TypeEmail), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if n := countPath(srv.Requests(), "/file/read"); n != 5 || len(results) != 5 {
		t.Errorf("%d file reads, %d results; want every file", n, len(results))
	}
	checkStats(t, stats, -1, LimitNone)
}

func TestBreachHouse_LimitFollowsMaxResults(t *testing.T) {
	cases := []struct {
		q     Query
		limit int
		want  Limit
	}{
		{Query{MaxResults: 50}, 50, LimitResults},
		{Query{}, 200, LimitAPI},
	}
	for _, c := range cases {
		srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			var items []string
			for i := 0; i < limit; i++ {
				items = append(items, fmt.Sprintf(`{"id":"r%d","name":"Victim %d","website":"example.com"}`, i, i))
			}
			_, _ = fmt.Fprintf(w, `{"ransomware":[%s],"breaches":[],"leads":[],"stealers":[]}`, strings.Join(items, ","))
		}))
		_, stats, err := searchSource(t, withKeys(&BreachHouse{}, testKey), query(c.q, "example.com", TypeDomain), srv.Transport())
		if err != nil {
			t.Fatal(err)
		}
		if got := srv.Requests()[0].URL.Query().Get("limit"); got != strconv.Itoa(c.limit) {
			t.Errorf("%+v: limit=%s, want %d", c.q, got, c.limit)
		}
		checkStats(t, stats, -1, c.want)
	}
}

func TestProxyNova_TruncatedAtTheAPICap(t *testing.T) {
	cases := []struct {
		q     Query
		limit int
		want  Limit
	}{
		{defaultQuery, 100, LimitAPI},
		{Query{MaxResults: 50}, 50, LimitResults},
	}
	for _, c := range cases {
		srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			var lines []string
			for i := 0; i < limit; i++ {
				lines = append(lines, fmt.Sprintf(`"user%d@example.com:pw"`, i))
			}
			_, _ = fmt.Fprintf(w, `{"count":523,"lines":[%s]}`, strings.Join(lines, ","))
		}))
		results, stats, err := searchSource(t, &ProxyNova{}, query(c.q, "example.com", TypeDomain), srv.Transport())
		if err != nil {
			t.Fatal(err)
		}
		if got := srv.Requests()[0].URL.Query().Get("limit"); got != strconv.Itoa(c.limit) || len(results) != c.limit {
			t.Errorf("%+v: limit=%s and %d results, want %d", c.q, got, len(results), c.limit)
		}
		checkStats(t, stats, 523, c.want)
	}
}

// leakCheckMock serves found records with limit/offset and reports a quota.
func leakCheckMock(t *testing.T, found int) *fakeapi.Server {
	t.Helper()
	return fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if limit < 1 || limit > 1000 || offset < 0 || offset > 2500 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"success":false,"error":"Invalid limit or offset"}`)
			return
		}
		var result []string
		for i := offset; i < found && i < offset+limit; i++ {
			result = append(result, fmt.Sprintf(`{"email":"user%d@example.com","password":"pw","fields":["email","password"]}`, i))
		}
		_, _ = fmt.Fprintf(w, `{"success":true,"found":%d,"quota":400,"result":[%s]}`, found, strings.Join(result, ","))
	}))
}

func TestLeakCheck_PagesWithOffset(t *testing.T) {
	srv := leakCheckMock(t, 2500)
	results, stats, err := searchSource(t, withKeys(&LeakCheck{}, testKey), query(Query{}, "example.com", TypeDomain), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2500 || len(srv.Requests()) != 3 {
		t.Errorf("%d results from %d requests, want 2500 from 3", len(results), len(srv.Requests()))
	}
	checkStats(t, stats, 2500, LimitNone)
	if stats.CreditsLeft == nil || *stats.CreditsLeft != 400 {
		t.Errorf("CreditsLeft = %v, want the quota of 400", stats.CreditsLeft)
	}

	// Offsets stop at 2,500: 3,000 of 5,000 is all the API serves.
	srv = leakCheckMock(t, 5000)
	results, stats, err = searchSource(t, withKeys(&LeakCheck{}, testKey), query(Query{}, "example.com", TypeDomain), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3000 || len(srv.Requests()) != 3 {
		t.Errorf("%d results from %d requests, want 3000 from 3", len(results), len(srv.Requests()))
	}
	checkStats(t, stats, 5000, LimitAPI)

	srv = leakCheckMock(t, 2500)
	results, stats, err = searchSource(t, withKeys(&LeakCheck{}, testKey), query(defaultQuery, "example.com", TypeDomain), srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1000 || len(srv.Requests()) != 1 {
		t.Errorf("%d results from %d requests, want 1000 from 1", len(results), len(srv.Requests()))
	}
	checkStats(t, stats, 2500, LimitResults)
}

func TestLeakCheck_ErrorKinds(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   error
	}{
		{http.StatusBadRequest, `{"success":false,"error":"Invalid API key"}`, ErrAuth},
		{http.StatusForbidden, `{"success":false,"error":"Limit reached"}`, ErrQuota},
	}
	for _, c := range cases {
		srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		}))
		_, _, err := searchSource(t, withKeys(&LeakCheck{}, testKey), query(defaultQuery, "jane@example.com", TypeEmail), srv.Transport())
		if !errors.Is(err, c.kind) || (c.kind == ErrQuota && errors.Is(err, ErrAuth)) {
			t.Errorf("status %d %s: err = %v, want %v", c.status, c.body, err, c.kind)
		}
	}
}

// TestSources_Limits: the page size and cap each API documents.
func TestSources_Limits(t *testing.T) {
	want := map[Source]Limits{
		&DeHashed{}:    {PageSize: 10000, MaxResults: 10000},
		&LeakRadar{}:   {PageSize: 100},
		&WhiteIntel{}:  {PageSize: 5000},
		&OSINTLeak{}:   {PageSize: 100},
		&IntelX{}:      {PageSize: 100, MaxResults: 1000, DefaultRequests: 10},
		&BreachHouse{}: {PageSize: 200, MaxResults: 200},
		&ProxyNova{}:   {PageSize: 100, MaxResults: 100},
		&LeakCheck{}:   {PageSize: 1000},
		&WeLeakInfo{}:  {PageSize: 1000, MaxResults: 1000},
		&HIBR{}:        {},
		&Snusbase{}:    {},
	}
	for src, limits := range want {
		if got := src.Limits(); got != limits {
			t.Errorf("%s.Limits() = %+v, want %+v", src.Name(), got, limits)
		}
	}
	if got := (&WhiteIntel{}).MinInterval(); got != 5*time.Second {
		t.Errorf("WhiteIntel.MinInterval() = %v, want 5s (\"Please wait 5 seconds between requests\")", got)
	}
}

// TestSingleAnswerSources_ReportTotal: one-answer APIs that say how many
// records matched report it. Snusbase's total covers its combo-lookup too,
// whose records it also returns: never "3 of 2".
func TestSingleAnswerSources_ReportTotal(t *testing.T) {
	for name, want := range map[string]int{"breachdirectory": 2, "snusbase": 3} {
		hc := happyCaseNamed(t, name)
		srv := fakeapi.NewServer(t, fixtureHandler(t, hc.dir, hc.routes))
		results, stats, err := searchSource(t, hc.source(), Query{Target: hc.target, Type: hc.st}, srv.Transport())
		if err != nil {
			t.Fatal(err)
		}
		if stats.Total != want {
			t.Errorf("%s: Total = %d, want %d", name, stats.Total, want)
		}
		if len(results) > stats.Total {
			t.Errorf("%s: %d results of a total of %d", name, len(results), stats.Total)
		}
	}
}

// TestPaging_RepeatingAPIsStop: --all against APIs that repeat themselves
// ends after a few requests instead of running until the quota is gone.
func TestPaging_RepeatingAPIsStop(t *testing.T) {
	all := func(target string, st ScanType) Query { return Query{Target: target, Type: st} }
	cases := []struct {
		name    string
		src     Source
		q       Query
		handler http.HandlerFunc
		max     int
	}{
		{"spycloud repeats its cursor", withKeys(&SpyCloud{}, testKey), all("example.com", TypeDomain),
			func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"cursor":"same","hits":10,"results":[{"email":"a@example.com","password":"p"}]}`)
			}, 2},
		{"flare repeats its cursor", withKeys(&Flare{}, testKey), all("example.com", TypeDomain),
			func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/tokens/generate" {
					_, _ = io.WriteString(w, `{"token":"T"}`)
					return
				}
				_, _ = io.WriteString(w, `{"items":[{"identity_name":"a@example.com","hash":"p","hash_type":null}],"next":"same"}`)
			}, 3},
		{"whiteintel ignores the page", withKeys(&WhiteIntel{}, testKey), all("example.com", TypeDomain),
			func(w http.ResponseWriter, r *http.Request) {
				var req whiteIntelRequest
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &req)
				var results []string
				for i := 0; i < req.Limit; i++ {
					results = append(results, fmt.Sprintf(`{"username":"user%d@example.com","password":"pw"}`, i))
				}
				_, _ = fmt.Fprintf(w, `{"success":true,"results":[%s]}`, strings.Join(results, ","))
			}, 2},
		{"hibr repeats its page", withKeys(&HIBR{}, testKey), all("example.com", TypeDomain),
			func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"sources":[{"id_source":"p1","post_title":"Example","website":"example.com"}],"has_next":true}`)
			}, 1 + maxEmptyPages},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := fakeapi.NewServer(t, c.handler)
			_, stats, err := searchSource(t, c.src, c.q, srv.Transport())
			if err != nil {
				t.Fatal(err)
			}
			if n := len(srv.Requests()); n > c.max {
				t.Errorf("%d requests, want at most %d", n, c.max)
			}
			if !stats.Truncated {
				t.Error("stopping a repeating API leaves results behind: truncated")
			}
		})
	}
}
