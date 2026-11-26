package sources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
)

// fastRetry keeps backoff short so retry tests run in milliseconds.
var fastRetry = RetryPolicy{BaseDelay: time.Millisecond}

// testSession returns a session on transport with fast retries.
func testSession(t *testing.T, transport http.RoundTripper) *Session {
	t.Helper()
	session, err := NewSession(SessionOptions{Timeout: 5 * time.Second, UserAgent: "leaker-test", Transport: transport, Retry: fastRetry})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// statusServer answers with statuses in order, then 200 with body "ok".
// Each answer can set headers through headers[i].
func statusServer(t *testing.T, statuses []int, headers ...http.Header) *fakeapi.Server {
	t.Helper()
	var calls atomic.Int32
	return fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := int(calls.Add(1)) - 1
		if i < len(headers) {
			for k, v := range headers[i] {
				w.Header()[k] = v
			}
		}
		if i < len(statuses) {
			w.WriteHeader(statuses[i])
			_, _ = fmt.Fprintf(w, "status %d", statuses[i])
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
}

func get(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// doBody sends req and returns the body of a successful response.
func doBody(t *testing.T, s *Session, req *http.Request) (string, error) {
	t.Helper()
	resp, err := s.Do(context.Background(), req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), nil
}

func TestSessionDo_Retries429ThenSucceeds(t *testing.T) {
	srv := statusServer(t, []int{429, 429})
	s := testSession(t, srv.Transport()).ForSource(0)

	body, err := doBody(t, s, get(t, "https://api.example.test/search?q=jane"))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if body != "ok" {
		t.Errorf("body %q, want the third answer", body)
	}
	if n := len(srv.Requests()); n != 3 {
		t.Errorf("server saw %d requests, want 3", n)
	}
	if n := s.Requests(); n != 3 {
		t.Errorf("Requests() = %d, want 3 (retries count)", n)
	}
}

func TestSessionDo_RetriesGatewayErrors(t *testing.T) {
	srv := statusServer(t, []int{502, 503, 504})
	if _, err := doBody(t, testSession(t, srv.Transport()), get(t, "https://api.example.test/")); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if n := len(srv.Requests()); n != 4 {
		t.Errorf("server saw %d requests, want 4", n)
	}
}

func TestSessionDo_GivesUpAfterThreeRetries(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := statusServer(t, []int{status, status, status, status, status})
			_, err := doBody(t, testSession(t, srv.Transport()), get(t, "https://api.example.test/"))
			var statusErr *StatusError
			if !errors.As(err, &statusErr) || statusErr.StatusCode != status {
				t.Fatalf("err = %v, want *StatusError %d", err, status)
			}
			if n := len(srv.Requests()); n != 4 {
				t.Errorf("server saw %d requests, want 4 (one try, three retries)", n)
			}
			if got, want := errors.Is(err, ErrRateLimited), status == 429; got != want {
				t.Errorf("errors.Is(err, ErrRateLimited) = %v, want %v", got, want)
			}
		})
	}
}

func TestSessionDo_NeverRetriesAuthOrQuota(t *testing.T) {
	cases := map[int]error{401: ErrAuth, 403: ErrAuth, 402: ErrQuota}
	for status, kind := range cases {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := statusServer(t, []int{status, status})
			_, err := doBody(t, testSession(t, srv.Transport()), get(t, "https://api.example.test/"))
			if !errors.Is(err, kind) {
				t.Errorf("err = %v, want %v", err, kind)
			}
			if n := len(srv.Requests()); n != 1 {
				t.Errorf("server saw %d requests, want 1", n)
			}
		})
	}
}

func TestSessionDo_OtherErrorsAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 404, 500} {
		srv := statusServer(t, []int{status, status})
		_, err := doBody(t, testSession(t, srv.Transport()), get(t, "https://api.example.test/"))
		var statusErr *StatusError
		if !errors.As(err, &statusErr) || statusErr.StatusCode != status {
			t.Errorf("status %d: err = %v, want *StatusError", status, err)
		}
		if errors.Is(err, ErrAuth) || errors.Is(err, ErrQuota) || errors.Is(err, ErrRateLimited) {
			t.Errorf("status %d must not be auth, quota or rate-limited: %v", status, err)
		}
		if n := len(srv.Requests()); n != 1 {
			t.Errorf("status %d: server saw %d requests, want 1", status, n)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("api.example.test returned status %d", status)) {
			t.Errorf("error %q should name the host and the status", err)
		}
	}
}

func TestSessionDo_HonoursRetryAfter(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	var calls atomic.Int32
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	if _, err := doBody(t, testSession(t, srv.Transport()), get(t, "https://api.example.test/")); err != nil {
		t.Fatalf("Do: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) != 2 {
		t.Fatalf("%d requests, want 2", len(times))
	}
	// Backoff alone is about a millisecond here; only Retry-After explains a second.
	if gap := times[1].Sub(times[0]); gap < time.Second {
		t.Errorf("retried after %v, want at least the 1s of Retry-After", gap)
	}
}

func TestSessionDo_LongRetryAfterGivesUp(t *testing.T) {
	srv := statusServer(t, []int{429, 429}, http.Header{"Retry-After": {"3600"}})
	start := time.Now()
	_, err := doBody(t, testSession(t, srv.Transport()), get(t, "https://api.example.test/"))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.RetryAfter != time.Hour {
		t.Errorf("RetryAfter should be 1h: %+v", statusErr)
	}
	if n := len(srv.Requests()); n != 1 {
		t.Errorf("server saw %d requests, want 1: waiting an hour is not an option", n)
	}
	if time.Since(start) > time.Second {
		t.Error("Do waited instead of giving up")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":                              0,
		"7":                             7 * time.Second,
		"-3":                            0,
		"soon":                          0,
		"Thu, 26 Nov 2026 12:00:30 GMT": 30 * time.Second,
		"Thu, 26 Nov 2026 11:00:00 GMT": 0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSessionDo_CancelStopsRetrying(t *testing.T) {
	srv := statusServer(t, []int{503, 503, 503, 503}, http.Header{"Retry-After": {"20"}})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.test/", nil)
	start := time.Now()
	_, err := testSession(t, srv.Transport()).Do(ctx, req)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Do returned after %v; cancelling must stop the wait", elapsed)
	}
}

func TestSessionDo_RetryResendsBody(t *testing.T) {
	srv := statusServer(t, []int{429})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.example.test/search",
		strings.NewReader(`{"query":"jane"}`))
	if _, err := doBody(t, testSession(t, srv.Transport()), req); err != nil {
		t.Fatalf("Do: %v", err)
	}
	requests := srv.Requests()
	if len(requests) != 2 {
		t.Fatalf("%d requests, want 2", len(requests))
	}
	for i, r := range requests {
		if string(r.Body) != `{"query":"jane"}` {
			t.Errorf("request %d body %q, want the original body", i, r.Body)
		}
	}
}

// TestSessionDo_StatusErrorHidesSecrets: some APIs and proxy block pages echo
// the request; the error never shows a key from a header, the query or the body.
func TestSessionDo_StatusErrorHidesSecrets(t *testing.T) {
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, "echo %s %s %v %s", r.Method, r.URL, r.Header, body)
	}))
	session := testSession(t, srv.Transport())

	query := get(t, "https://api.example.test/search?api_key=SECRET_QUERY_1&q=jane")
	headers := get(t, "https://api.example.test/search")
	headers.Header.Set("X-API-Key", "SECRET_HEADER_2")
	headers.Header.Set("Authorization", "Bearer SECRET_BEARER_3")
	headers.Header.Set("Auth", "SECRET_AUTH_4")
	jsonReq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.example.test/search",
		strings.NewReader(`{"apikey":"SECRET_JSON_5","query":"jane"}`))
	jsonReq.Header.Set("Content-Type", "application/json")
	form := url.Values{"key": {"SECRET_FORM_6"}, "query": {"jane"}}
	formReq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.example.test/search",
		strings.NewReader(form.Encode()))
	formReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	for _, req := range []*http.Request{query, headers, jsonReq, formReq} {
		_, err := doBody(t, session, req)
		if err == nil {
			t.Fatal("a 500 must be an error")
		}
		if strings.Contains(err.Error(), "SECRET_") {
			t.Errorf("error leaks a secret: %v", err)
		}
		if !strings.Contains(err.Error(), "REDACTED") {
			t.Errorf("error should still quote the redacted body: %v", err)
		}
	}
}

// TestSession_ReusesConnections: requests to one host share a connection.
func TestSession_ReusesConnections(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	session := testSession(t, srv.Client().Transport)
	for i := 0; i < 3; i++ {
		if _, err := doBody(t, session, get(t, srv.URL)); err != nil {
			t.Fatal(err)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("3 requests opened %d connections, want 1", n)
	}
}

func TestSessionDo_WaitsOnSourceLimiter(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	}))
	base := testSession(t, srv.Transport())
	limited := base.ForSource(50 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if _, err := doBody(t, limited, get(t, "https://api.example.test/")); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < 45*time.Millisecond {
			t.Errorf("requests %d and %d were %v apart, want about 50ms", i-1, i, gap)
		}
	}
	if limited.Requests() != 3 || base.Requests() != 0 {
		t.Errorf("each handle counts its own requests: limited %d, base %d", limited.Requests(), base.Requests())
	}
}

// keyForms are the ways an echoed request can show the key abc/def+ghi==.
var keyForms = []string{"abc/def+ghi==", "abc%2Fdef%2Bghi%3D%3D", `abc\/def+ghi==`, "abc%2Fdef+ghi=="}

func assertNoKeyForm(t *testing.T, what, text string) {
	t.Helper()
	for _, form := range keyForms {
		if strings.Contains(text, form) {
			t.Errorf("%s shows the key as %q: %s", what, form, text)
		}
	}
}

// TestErrorBody_RedactsEncodedKeys: an API that echoes the request may show
// the key URL-, form- or JSON-encoded.
func TestErrorBody_RedactsEncodedKeys(t *testing.T) {
	for _, body := range []string{
		"bad request body was: key=abc%2Fdef%2Bghi%3D%3D&query=jane",
		`{"apikey":"abc\/def+ghi==","query":"jane"}`,
		`{"apikey":"abc/def+ghi==","query":"jane"}`,
		"no route for /v1/abc%2Fdef+ghi==/search",
	} {
		assertNoKeyForm(t, "errorBody", errorBody([]byte(body), "abc/def+ghi=="))
	}
}

func TestRedactSecrets_ParametersAnywhere(t *testing.T) {
	cases := map[string]string{
		"key=SECRET&query=jane":         "key=REDACTED&query=jane",
		"echo: api_key=SECRET q=1":      "echo: api_key=REDACTED q=1",
		`{"apikey":"SEC\"RET","q":"x"}`: `{"apikey":"REDACTED","q":"x"}`,
		`{"token": "SECRET"}`:           `{"token": "REDACTED"}`,
		"monkey=banana keyword=ok":      "monkey=banana keyword=ok",
	}
	for in, want := range cases {
		if got := RedactSecrets(in); got != want {
			t.Errorf("RedactSecrets(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// TestSessionDo_StatusErrorHidesEncodedKey: a form key with / + = that the
// API echoes encoded never shows in the error.
func TestSessionDo_StatusErrorHidesEncodedKey(t *testing.T) {
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, "bad request body was: %s", body)
	}))
	form := url.Values{"key": {"abc/def+ghi=="}, "query": {"jane"}}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.example.test/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, err := doBody(t, testSession(t, srv.Transport()), req)
	if err == nil {
		t.Fatal("a 400 must be an error")
	}
	assertNoKeyForm(t, "the error", err.Error())
}

// TestLeakLookup_ErrorMessageHidesKey: LeakLookup reports some errors in a
// 200 answer; its message may name the key.
func TestLeakLookup_ErrorMessageHidesKey(t *testing.T) {
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"error":"true","message":"API key abc\/def+ghi== is not valid"}`)
	}))
	_, errs := runSource(t, withKeys(&LeakLookup{}, "abc/def+ghi=="), "jane@example.com", TypeEmail, srv.Transport())
	if len(errs) != 1 {
		t.Fatalf("errors %v, want one", errs)
	}
	assertNoKeyForm(t, "the error", errs[0].Error())
}

// TestSession_DoesNotFollowRedirects: no source relies on a redirect, and a
// followed one would carry custom key headers to the host it names. A
// redirect, to another host or the same one, is an error naming its target.
func TestSession_DoesNotFollowRedirects(t *testing.T) {
	for _, location := range []string{"https://evil.example.test/steal", "https://api.example.test/v2/search"} {
		srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != "api.example.test" || r.URL.Path != "/search" {
				t.Errorf("followed the redirect to %s%s with X-API-Key %q", r.Host, r.URL.Path, r.Header.Get("X-API-Key"))
			}
			http.Redirect(w, r, location, http.StatusFound)
		}))
		req := get(t, "https://api.example.test/search")
		req.Header.Set("X-API-Key", "SECRET_REDIRECT_KEY")
		_, err := doBody(t, testSession(t, srv.Transport()), req)
		var statusErr *StatusError
		if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusFound {
			t.Fatalf("err = %v, want *StatusError 302", err)
		}
		if !strings.Contains(err.Error(), "redirect to "+location+" not followed") {
			t.Errorf("error %q should name the redirect", err)
		}
		if n := len(srv.Requests()); n != 1 {
			t.Errorf("%d requests, want 1", n)
		}
	}
}
