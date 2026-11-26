package sources

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/vflame6/leaker/internal/fakeapi"
)

// The SpyCloud and Flare fixtures follow the vendors' documented responses;
// no live answer has been recorded yet.

func TestSpyCloud_Email(t *testing.T) {
	srv := fakeapi.NewServer(t, fixtureHandler(t, "spycloud", map[string]string{
		"GET api.spycloud.io/investigations-v2/breach/data/emails/jane@example.com": "email.json",
	}))
	results, stats, err := searchSource(t, withKeys(&SpyCloud{}, testKey), Query{Target: "jane@example.com", Type: TypeEmail}, srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{
		{Source: "spycloud", Email: "jane@example.com", Username: "jane", Password: "hunter2", IP: "203.0.113.10",
			URL: "https://login.example.com/", Database: "Example Stealer Logs", Extra: map[string]string{
				"breach_category": "infostealer", "severity": "25", "published": "2026-10-20", "source_id": "234505",
				"user_hostname": "DESKTOP-1", "user_os": "Windows 10"}},
		// A cracked hash: the hash and the plaintext SpyCloud recovered. The
		// masked username (outside the contract) is left out.
		{Source: "spycloud", Email: "jane@example.com", Password: "password", Hash: "5f4dcc3b5aa765d61d8327deb882cf99",
			Salt: "x1", Name: "Jane Doe", Phone: "5552341096", Database: "Example Forum", Extra: map[string]string{
				"breach_category": "exfiltrated", "severity": "20", "published": "2026-04-21", "source_id": "152719",
				"password_type": "md5"}},
	}
	if !reflect.DeepEqual(results, want) {
		t.Errorf("results differ\n got: %+v\nwant: %+v", results, want)
	}
	checkStats(t, stats, 2, LimitNone)
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	expectHeader(t, reqs[0], "X-Api-Key", testKey)
	if reqs[0].URL.Query().Has("cursor") {
		t.Error("the first page has no cursor")
	}
}

func TestSpyCloud_PathPerType(t *testing.T) {
	cases := map[ScanType]string{
		TypeEmail:    "/investigations-v2/breach/data/emails/jane%2Btag@example.com",
		TypeUsername: "/investigations-v2/breach/data/usernames/acme%20corp",
		TypeDomain:   "/investigations-v2/breach/data/domains/example.com",
		TypePhone:    "/investigations-v2/breach/data/phone-numbers/15552341096",
	}
	targets := map[ScanType]string{TypeEmail: "jane+tag@example.com", TypeUsername: "acme corp", TypeDomain: "example.com", TypePhone: "15552341096"}
	for st, path := range cases {
		srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"cursor":"","hits":0,"results":[]}`)
		}))
		if _, _, err := searchSource(t, withKeys(&SpyCloud{}, testKey), Query{Target: targets[st], Type: st}, srv.Transport()); err != nil {
			t.Fatal(err)
		}
		if got := srv.Requests()[0].URL.EscapedPath(); got != path {
			t.Errorf("%s: path %s, want %s", st, got, path)
		}
	}
}

// TestSpyCloud_CursorPaging: a non-empty cursor means another page.
func TestSpyCloud_CursorPaging(t *testing.T) {
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = io.WriteString(w, `{"cursor":"page-2","hits":2,"results":[{"email":"a@example.com","password":"p1","password_type":"plaintext"}]}`)
		case "page-2":
			_, _ = io.WriteString(w, `{"cursor":"","hits":2,"results":[{"email":"b@example.com","password":"p2","password_type":"plaintext"}]}`)
		default:
			http.Error(w, "bad cursor", http.StatusBadRequest)
		}
	}))
	results, stats, err := searchSource(t, withKeys(&SpyCloud{}, testKey), Query{Target: "example.com", Type: TypeDomain, MaxRequests: 3}, srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || len(srv.Requests()) != 2 {
		t.Errorf("%d results from %d requests, want 2 from 2", len(results), len(srv.Requests()))
	}
	checkStats(t, stats, 2, LimitNone)
}

func TestSpyCloud_MonthlyLimitIsQuota(t *testing.T) {
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"Limit exceeded"}`)
	}))
	_, _, err := searchSource(t, withKeys(&SpyCloud{}, testKey), Query{Target: "jane@example.com", Type: TypeEmail}, srv.Transport())
	if !errors.Is(err, ErrQuota) || errors.Is(err, ErrRateLimited) {
		t.Errorf("err = %v, want quota (the monthly limit), not a rate limit", err)
	}
}

// flareAPI serves the token and two pages of credentials.
func flareAPI(t *testing.T, tokens *atomic.Int32) *fakeapi.Server {
	t.Helper()
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join("testdata", "flare", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	return fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tokens/generate":
			tokens.Add(1)
			if r.Header.Get("Authorization") != testKey {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write(read("token.json"))
		case "/astp/v2/credentials/_search":
			if r.Header.Get("Authorization") != "Bearer FLARE_SESSION_TOKEN_1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["from"] == nil {
				_, _ = w.Write(read("page1.json"))
			} else {
				_, _ = w.Write(read("page2.json"))
			}
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestFlare_DomainCredentials(t *testing.T) {
	var tokens atomic.Int32
	srv := flareAPI(t, &tokens)
	results, stats, err := searchSource(t, withKeys(&Flare{}, testKey), Query{Target: "example.com", Type: TypeDomain}, srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{
		{Source: "flare", Email: "jane@example.com", Password: "B@dPassw0rd", Database: "Combolists",
			Extra: map[string]string{"imported_at": "2024-07-22", "auth_domains": "login.example.com"}},
		{Source: "flare", Email: "bob@example.com", Hash: "$2b$12$C6UzMDM.H6dfI/f/IKcEeO", Database: "Example Forum",
			Extra: map[string]string{"imported_at": "2025-01-05", "hash_type": "bcrypt"}},
	}
	if !reflect.DeepEqual(results, want) {
		t.Errorf("results differ\n got: %+v\nwant: %+v", results, want)
	}
	checkStats(t, stats, -1, LimitNone)

	var searches []map[string]any
	for _, r := range srv.Requests() {
		if r.URL.Path == "/astp/v2/credentials/_search" {
			var body map[string]any
			if err := json.Unmarshal(r.Body, &body); err != nil {
				t.Fatal(err)
			}
			searches = append(searches, body)
		}
	}
	if len(searches) != 2 {
		t.Fatalf("%d search requests, want 2 (cursor paging)", len(searches))
	}
	query, _ := searches[0]["query"].(map[string]any)
	if query["type"] != "domain" || query["fqdn"] != "example.com" {
		t.Errorf("query %v, want a domain query for example.com", query)
	}
	if searches[1]["from"] != "WyJjb20uZXhhbXBsZSIsIDMzODgwNzAzOTA3XQ" {
		t.Errorf("the second page must send the first page's next as from: %v", searches[1])
	}
	if n := tokens.Load(); n != 1 {
		t.Errorf("%d token requests, want 1", n)
	}
}

func TestFlare_EmailQueryAndCachedToken(t *testing.T) {
	var tokens atomic.Int32
	srv := flareAPI(t, &tokens)
	src := withKeys(&Flare{}, testKey)
	for i := 0; i < 2; i++ {
		if _, _, err := searchSource(t, src, Query{Target: "jane@example.com", Type: TypeEmail, MaxRequests: 1}, srv.Transport()); err != nil {
			t.Fatal(err)
		}
	}
	if n := tokens.Load(); n != 1 {
		t.Errorf("%d token requests for two searches, want 1 (tokens last an hour)", n)
	}
	for _, r := range srv.Requests() {
		if r.URL.Path != "/astp/v2/credentials/_search" {
			continue
		}
		var body struct {
			Query map[string]string `json:"query"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil || body.Query["type"] != "email" || body.Query["email"] != "jane@example.com" {
			t.Errorf("body %s, want an email query for jane@example.com", r.Body)
		}
	}
}

// TestFlare_EmptyPageWithCursorIsNotTheEnd: Flare documents that empty pages
// do not mean the search is over; only a missing next does.
func TestFlare_EmptyPageWithCursorIsNotTheEnd(t *testing.T) {
	var calls atomic.Int32
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tokens/generate" {
			_, _ = io.WriteString(w, `{"token":"T"}`)
			return
		}
		switch calls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, `{"items":[],"next":"c2"}`)
		default:
			_, _ = io.WriteString(w, `{"items":[{"identity_name":"a@example.com","hash":"p","hash_type":null,"source":{"name":"S"}}],"next":null}`)
		}
	}))
	results, _, err := searchSource(t, withKeys(&Flare{}, testKey), Query{Target: "example.com", Type: TypeDomain, MaxRequests: 5}, srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || calls.Load() != 2 {
		t.Errorf("%d results from %d pages, want 1 from 2", len(results), calls.Load())
	}
}

func TestEnterpriseSources_Declarations(t *testing.T) {
	cases := []struct {
		src    Source
		types  []ScanType
		limits Limits
	}{
		{&SpyCloud{}, []ScanType{TypeEmail, TypeUsername, TypeDomain, TypePhone}, Limits{PageSize: 1000}},
		{&Flare{}, []ScanType{TypeEmail, TypeDomain}, Limits{PageSize: 10000}},
	}
	for _, c := range cases {
		if got := c.src.Types(); !reflect.DeepEqual(got, c.types) {
			t.Errorf("%s.Types() = %v, want %v", c.src.Name(), got, c.types)
		}
		if got := c.src.Limits(); got != c.limits {
			t.Errorf("%s.Limits() = %+v, want %+v", c.src.Name(), got, c.limits)
		}
		if !c.src.UsesKey() || !c.src.NeedsKey() || c.src.MinInterval().Seconds() != 1 {
			t.Errorf("%s needs a key and waits a second between requests", c.src.Name())
		}
	}
}
