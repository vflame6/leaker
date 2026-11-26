package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
)

// testKey is the API key given to every source in these tests. It must never
// appear in an error.
const testKey = "SECRET_TEST_KEY_123"

// sourceCase describes how to drive one source in the table tests below.
type sourceCase struct {
	name string
	// newSource returns a fresh source with testKey configured.
	newSource func() Source
	// emptyBody is a successful response with no records.
	emptyBody func(r *http.Request) string
	// target extracts the searched value from a request, decoded the way the
	// API decodes it. ok is false for requests that do not carry the target.
	target func(r *http.Request, body []byte, st ScanType) (value string, ok bool)
	// domainOnly sources accept plain domain names only.
	domainOnly bool
}

func constBody(body string) func(*http.Request) string {
	return func(*http.Request) string { return body }
}

func queryParam(name string) func(*http.Request, []byte, ScanType) (string, bool) {
	return func(r *http.Request, _ []byte, _ ScanType) (string, bool) {
		values, ok := r.URL.Query()[name]
		if !ok || len(values) != 1 {
			return "", false
		}
		return values[0], true
	}
}

func jsonField(field string) func(*http.Request, []byte, ScanType) (string, bool) {
	return func(_ *http.Request, body []byte, _ ScanType) (string, bool) {
		var m map[string]interface{}
		if json.Unmarshal(body, &m) != nil {
			return "", false
		}
		switch v := m[field].(type) {
		case string:
			return v, true
		case []interface{}:
			if len(v) == 1 {
				s, ok := v[0].(string)
				return s, ok
			}
		}
		return "", false
	}
}

// pathSegmentAfter decodes the path segment that follows prefix.
func pathSegmentAfter(prefix string) func(*http.Request, []byte, ScanType) (string, bool) {
	return func(r *http.Request, _ []byte, _ ScanType) (string, bool) {
		escaped := r.URL.EscapedPath()
		if !strings.HasPrefix(escaped, prefix) {
			return "", false
		}
		segment, _, _ := strings.Cut(strings.TrimPrefix(escaped, prefix), "/")
		decoded, err := url.PathUnescape(segment)
		return decoded, err == nil
	}
}

func withKeys(s Source, keys ...string) Source {
	s.AddApiKeys(keys)
	return s
}

func sourceCases() []sourceCase {
	return []sourceCase{
		{
			name:      "breachdirectory",
			newSource: func() Source { return withKeys(&BreachDirectory{}, testKey) },
			emptyBody: constBody(`{"found":0,"result":[]}`),
			target:    queryParam("term"),
		},
		{
			name:       "breachhouse",
			newSource:  func() Source { return withKeys(&BreachHouse{}, testKey) },
			emptyBody:  constBody(`{"ransomware":[],"breaches":[],"leads":[],"stealers":[]}`),
			target:     queryParam("q"),
			domainOnly: true,
		},
		{
			name:      "dehashed",
			newSource: func() Source { return withKeys(&DeHashed{}, testKey) },
			emptyBody: constBody(`{"balance":0,"entries":[],"total":0}`),
			target: func(r *http.Request, body []byte, st ScanType) (string, bool) {
				q, ok := jsonField("query")(r, body, st)
				if st == TypeEmail {
					return strings.TrimPrefix(q, "email:"), ok && strings.HasPrefix(q, "email:")
				}
				return q, ok
			},
		},
		{
			name:       "hibr",
			newSource:  func() Source { return withKeys(&HIBR{noDelay: true}, testKey) },
			emptyBody:  constBody(`{"sources":[],"has_next":false}`),
			target:     pathSegmentAfter("/api/metadata/domain/"),
			domainOnly: true,
		},
		{
			name:      "hudsonrock",
			newSource: func() Source { return &HudsonRock{} },
			emptyBody: constBody(`{"stealers":[]}`),
			target: func(r *http.Request, body []byte, st ScanType) (string, bool) {
				if st == TypeEmail {
					return queryParam("email")(r, body, st)
				}
				return queryParam("username")(r, body, st)
			},
		},
		{
			name:      "hudsonrock (paid)",
			newSource: func() Source { return withKeys(&HudsonRock{}, testKey) },
			emptyBody: constBody(`{"data":[]}`),
			target:    queryParam("query"),
		},
		{
			name:      "intelx",
			newSource: func() Source { return withKeys(&IntelX{pollDelay: time.Millisecond}, "free.intelx.io:"+testKey) },
			emptyBody: func(r *http.Request) string {
				if strings.HasSuffix(r.URL.Path, "/intelligent/search") {
					return `{"id":"search-1","status":0}`
				}
				return `{"records":[],"status":1}`
			},
			target: func(r *http.Request, body []byte, st ScanType) (string, bool) {
				if r.Method != http.MethodPost {
					return "", false
				}
				return jsonField("term")(r, body, st)
			},
		},
		{
			name:      "leakcheck",
			newSource: func() Source { return withKeys(&LeakCheck{}, testKey) },
			emptyBody: constBody(`{"success":true,"found":0,"result":[]}`),
			target:    pathSegmentAfter("/api/v2/query/"),
		},
		{
			name:      "leaklookup",
			newSource: func() Source { return withKeys(&LeakLookup{}, testKey) },
			emptyBody: constBody(`{"error":"false","message":{}}`),
			target: func(_ *http.Request, body []byte, _ ScanType) (string, bool) {
				form, err := url.ParseQuery(string(body))
				if err != nil {
					return "", false
				}
				values, ok := form["query"]
				return strings.Join(values, ","), ok
			},
		},
		{
			name:      "leakradar",
			newSource: func() Source { return withKeys(&LeakRadar{}, testKey) },
			emptyBody: constBody(`{"items":[],"total":0,"page":1,"page_size":100}`),
			target:    jsonField("email"),
		},
		{
			name:      "leaksight",
			newSource: func() Source { return withKeys(&LeakSight{}, testKey) },
			emptyBody: constBody(`{}`),
			target:    queryParam("text"),
		},
		{
			name:      "osintleak",
			newSource: func() Source { return withKeys(&OSINTLeak{}, testKey) },
			emptyBody: constBody(`{"data":[]}`),
			target:    queryParam("query"),
		},
		{
			name:      "proxynova",
			newSource: func() Source { return &ProxyNova{} },
			emptyBody: constBody(`{"count":0,"lines":[]}`),
			target:    queryParam("query"),
		},
		{
			name:      "snusbase",
			newSource: func() Source { return withKeys(&Snusbase{}, testKey) },
			emptyBody: constBody(`{"took":0,"size":0,"results":{}}`),
			target:    jsonField("terms"),
		},
		{
			name:      "weleakinfo",
			newSource: func() Source { return withKeys(&WeLeakInfo{}, "pub:"+testKey) },
			emptyBody: constBody(`{"Data":[]}`),
			target:    jsonField("query"),
		},
		{
			name:      "whiteintel",
			newSource: func() Source { return withKeys(&WhiteIntel{}, testKey) },
			emptyBody: constBody(`{"success":true,"results":[]}`),
			target:    jsonField("query"),
		},
	}
}

// runSource runs src through a session on transport and splits what it
// returns into data results and errors.
func runSource(t *testing.T, src Source, target string, st ScanType, transport http.RoundTripper) ([]Result, []error) {
	t.Helper()
	session, err := NewSession(SessionOptions{Timeout: 5 * time.Second, UserAgent: "leaker-test", Transport: transport})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var results []Result
	var errs []error
	for r := range src.Run(ctx, target, st, session) {
		if r.Error != nil {
			errs = append(errs, r.Error)
			continue
		}
		results = append(results, r)
	}
	if ctx.Err() != nil {
		t.Fatalf("%s did not finish: %v", src.Name(), ctx.Err())
	}
	return results, errs
}

// TestSources_SpecialCharacters sends targets with reserved, space and
// non-ASCII characters through every source and checks that the fake API
// decodes each back to the exact target.
func TestSources_SpecialCharacters(t *testing.T) {
	targets := []struct {
		value string
		st    ScanType
	}{
		{"jane+tag@example.com", TypeEmail},
		{"acme corp", TypeKeyword},
		{"a&b=c#d", TypeKeyword},
		{"ключ café", TypeKeyword},
	}

	for _, sc := range sourceCases() {
		for _, tg := range targets {
			t.Run(sc.name+"/"+tg.value, func(t *testing.T) {
				srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.WriteString(w, sc.emptyBody(r))
				}))

				st := tg.st
				if sc.domainOnly {
					st = TypeDomain
				}
				_, errs := runSource(t, sc.newSource(), tg.value, st, srv.Transport())
				requests := srv.Requests()

				if sc.domainOnly {
					if len(requests) != 0 || len(errs) == 0 {
						t.Fatalf("domain-only source must reject %q without a request: %d request(s), errors %v", tg.value, len(requests), errs)
					}
					return
				}

				carried := 0
				for _, req := range requests {
					r := &http.Request{Method: req.Method, URL: req.URL, Header: req.Header}
					got, ok := sc.target(r, req.Body, st)
					if !ok {
						continue
					}
					carried++
					if got != tg.value {
						t.Errorf("%s %s decoded the target as %q, want %q", req.Method, req.URL, got, tg.value)
					}
				}
				if carried == 0 {
					t.Fatalf("no request carried the target; requests: %v", requestURLs(requests))
				}
				for _, err := range errs {
					t.Errorf("unexpected error: %v", err)
				}
			})
		}
	}
}

// TestSources_ErrorStatus checks that an API failure is reported as an error
// result, never as an empty success, and that the error hides the key.
func TestSources_ErrorStatus(t *testing.T) {
	for _, sc := range sourceCases() {
		t.Run(sc.name, func(t *testing.T) {
			// Like a proxy block page, the error body echoes the request,
			// so a key in the URL, a header or the body would leak through it.
			srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprintf(w, "You are limited to 100 results. Blocked %s %s headers=%v body=%s", r.Method, r.URL, r.Header, body)
			}))
			target, st := "jane@example.com", TypeEmail
			if sc.domainOnly {
				target, st = "example.com", TypeDomain
			}
			results, errs := runSource(t, sc.newSource(), target, st, srv.Transport())
			if len(errs) == 0 {
				t.Fatalf("an HTTP 429 must produce an error result, got %d result(s) and no error", len(results))
			}
			if len(results) != 0 {
				t.Errorf("expected no data results, got %v", results)
			}
			assertNoKey(t, errs)
		})
	}
}

// TestSources_TransportErrorHidesKey fails every request at the transport, as
// a dead proxy does, and checks that no error carries the API key.
func TestSources_TransportErrorHidesKey(t *testing.T) {
	for _, sc := range sourceCases() {
		t.Run(sc.name, func(t *testing.T) {
			transport := fakeapi.FailingTransport{Err: errors.New("proxyconnect tcp: connection refused")}
			target, st := "jane@example.com", TypeEmail
			if sc.domainOnly {
				target, st = "example.com", TypeDomain
			}
			_, errs := runSource(t, sc.newSource(), target, st, transport)
			if len(errs) == 0 {
				t.Fatal("a failed request must produce an error result")
			}
			assertNoKey(t, errs)
		})
	}
}

func assertNoKey(t *testing.T, errs []error) {
	t.Helper()
	for _, err := range errs {
		if strings.Contains(err.Error(), testKey) {
			t.Errorf("error leaks the API key: %v", err)
		}
	}
}

func requestURLs(requests []fakeapi.Request) []string {
	var urls []string
	for _, r := range requests {
		urls = append(urls, r.Method+" "+r.URL.String())
	}
	return urls
}

// TestSources_EveryRequestGoesThroughSessionDo: Session.Do is the one place
// that keeps keys out of errors and logs, so no source may bypass it.
func TestSources_EveryRequestGoesThroughSessionDo(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "session.go" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "Client.Do(") {
			t.Errorf("%s calls Client.Do directly; use session.Do", f)
		}
	}
}
