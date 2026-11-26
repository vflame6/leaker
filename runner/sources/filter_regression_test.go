package sources

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
)

// kept runs src against handler and returns the results the default filter
// keeps, as the runner would print them.
func kept(t *testing.T, src Source, target string, st ScanType, handler http.Handler) []Result {
	t.Helper()
	srv := fakeapi.NewServer(t, handler)
	results, errs := runSource(t, src, target, st, srv.Transport())
	for _, err := range errs {
		t.Fatalf("unexpected error: %v", err)
	}
	var out []Result
	for _, r := range results {
		r.TrimSpaces()
		if r.Matches(st, target) {
			out = append(out, r)
		}
	}
	return out
}

// intelxFile serves one IntelX search whose single leak file holds content.
func intelxFile(content string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/intelligent/search":
			_, _ = io.WriteString(w, `{"id":"s-1","status":0}`)
		case "/intelligent/search/result":
			_, _ = io.WriteString(w, `{"records":[{"storageid":"st-1","name":"dump.txt","bucket":"leaks.private","type":0}],"status":1}`)
		case "/file/read":
			_, _ = io.WriteString(w, content)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	})
}

func newIntelX() Source {
	return withKeys(&IntelX{pollDelay: time.Millisecond}, "free.intelx.io:"+testKey)
}

// TestFilter_KeepsIntelXEmailLines: quoted CSV and tab-, pipe- or
// space-separated dumps keep their records for an email search.
func TestFilter_KeepsIntelXEmailLines(t *testing.T) {
	content := strings.Join([]string{
		`1,'jane@example.com','5f4dcc3b5aa765d61d8327deb882cf99'`,
		`2,"jane@example.com","Jane Doe"`,
		"jane@example.com\thunter2",
		"jane@example.com | hunter3",
		"jane@example.com hunter4",
		"xjane@example.com:not-her",
	}, "\n")
	got := kept(t, newIntelX(), "jane@example.com", TypeEmail, intelxFile(content))
	want := []Result{
		{Source: "intelx", Email: "jane@example.com", Hash: "5f4dcc3b5aa765d61d8327deb882cf99"},
		{Source: "intelx", Email: "jane@example.com", Name: "Jane Doe"},
		{Source: "intelx", Email: "jane@example.com", Password: "hunter2"},
		{Source: "intelx", Email: "jane@example.com", Password: "hunter3"},
		{Source: "intelx", Email: "jane@example.com", Password: "hunter4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kept\n%+v\nwant\n%+v", got, want)
	}
}

// TestFilter_KeepsIntelXStealerLines: URL:LOGIN:PASSWORD lines keep their
// records for a domain search.
func TestFilter_KeepsIntelXStealerLines(t *testing.T) {
	content := strings.Join([]string{
		"https://login.example.com/signin:john:hunter2",
		"login.example.com:mary@corp.test:hunter3",
		"https://example.com.evil.org/login:bob:pw",
		"bob@example.com:pw2",
	}, "\n")
	got := kept(t, newIntelX(), "example.com", TypeDomain, intelxFile(content))
	want := []Result{
		{Source: "intelx", URL: "https://login.example.com/signin", Username: "john", Password: "hunter2"},
		{Source: "intelx", URL: "login.example.com", Email: "mary@corp.test", Password: "hunter3"},
		{Source: "intelx", Email: "bob@example.com", Password: "pw2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kept\n%+v\nwant\n%+v", got, want)
	}
}

// TestFilter_KeepsDarkEyeListings: listings that name the domain only in a
// title or in a free-text website are not lost.
func TestFilter_KeepsDarkEyeListings(t *testing.T) {
	breachHouse := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ransomware":[{"victim_id":"v1","post_title":"example.com","website":""},
			{"victim_id":"v2","post_title":"Evil Corp","website":"https://evil.org"}],"breaches":[],"leads":[],"stealers":[]}`)
	})
	got := kept(t, withKeys(&BreachHouse{}, testKey), "example.com", TypeDomain, breachHouse)
	if len(got) != 1 || got[0].Database != "example.com" {
		t.Errorf("Breach House kept %+v, want only the example.com listing", got)
	}

	hibr := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"sources":[{"id_source":"h1","leak_type":"ransomware_leak","post_title":"Example Corp",
			"website":"www.example.com (Example Corp)"}],"has_next":false}`)
	})
	got = kept(t, withKeys(&HIBR{noDelay: true}, testKey), "example.com", TypeDomain, hibr)
	if len(got) != 1 || got[0].Extra["website"] != "www.example.com (Example Corp)" {
		t.Errorf("HIBR kept %+v, want the listing with its website kept as extra", got)
	}
}
