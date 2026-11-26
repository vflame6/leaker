package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
	"github.com/vflame6/leaker/runner/sources"
)

// SHA-1("password") = 5BAA61E4C9B93F3F0682250B6CF8331B7EE68FD8
const passwordSuffix = "1E4C9B93F3F0682250B6CF8331B7EE68FD8"

func testSession(t *testing.T, transport http.RoundTripper) *sources.Session {
	t.Helper()
	session, err := sources.NewSession(sources.SessionOptions{Timeout: 5 * time.Second, UserAgent: "leaker-test", Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func hibpServer(t *testing.T, status int) *fakeapi.Server {
	return fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.pwnedpasswords.com" {
			t.Errorf("unexpected request to %s", r.Host)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "0018A45C4D1DEF81644B54AB7F969B88D65:1\r\n"+passwordSuffix+":10434004\r\n")
	}))
}

func TestVerifier_CountsFromRange(t *testing.T) {
	srv := hibpServer(t, http.StatusOK)
	v := NewVerifier(true, testSession(t, srv.Transport()))

	result := sources.Result{Password: "password", Hash: "5f4dcc3b5aa765d61d8327deb882cf99"}
	v.EnrichResult(context.Background(), &result)

	if got := result.Extra["hibp_count"]; got != "10434004" {
		t.Errorf("hibp_count = %q, want 10434004 (extra %v)", got, result.Extra)
	}
	if got := result.Extra["hash_type"]; got != "md5" {
		t.Errorf("hash_type = %q, want md5", got)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].URL.Path != "/range/5BAA6" {
		t.Fatalf("requests %v, want one GET /range/5BAA6", requestPaths(reqs))
	}
	if reqs[0].Header.Get("Add-Padding") != "true" {
		t.Error("the range request must ask for padding")
	}
}

// TestVerifier_FailureSetsErrorNotCount: a failed lookup must not look like
// "this password was never breached".
func TestVerifier_FailureSetsErrorNotCount(t *testing.T) {
	srv := hibpServer(t, http.StatusServiceUnavailable)
	v := NewVerifier(true, testSession(t, srv.Transport()))

	for i := 0; i < 2; i++ {
		result := sources.Result{Password: "password"}
		v.EnrichResult(context.Background(), &result)
		if _, ok := result.Extra["hibp_count"]; ok {
			t.Errorf("lookup %d: hibp_count set after a failure: %v", i, result.Extra)
		}
		if !strings.Contains(result.Extra["hibp_error"], "503") {
			t.Errorf("lookup %d: hibp_error = %q, want the failure", i, result.Extra["hibp_error"])
		}
	}
	if n := len(srv.Requests()); n != 2 {
		t.Errorf("failures must not be cached: %d requests, want 2", n)
	}
}

// TestEnumerate_VerifierSharedAcrossTargets: one verifier per run, so a
// password seen for several targets is looked up once.
func TestEnumerate_VerifierSharedAcrossTargets(t *testing.T) {
	captureLogs(t)
	srv := hibpServer(t, http.StatusOK)
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.NoFilter = true
	r.options.Verify = true
	r.session = testSession(t, srv.Transport())
	r.verifier = NewVerifier(true, r.session)
	r.scanSources = []sources.Source{&fakeSource{name: "s", emits: []sources.Result{{Source: "s", Email: "x@example.com", Password: "password"}}}}

	for _, target := range []string{"a@example.com", "b@example.com"} {
		if err := r.EnumerateSingleTarget(context.Background(), target, sources.TypeEmail, []io.Writer{&bytes.Buffer{}}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(srv.Requests()); n != 1 {
		t.Errorf("%d HIBP requests for one password, want 1", n)
	}
}

// TestProxyFlag_EveryHostUsesProxy: with --proxy, every source and the HIBP
// lookup go through the proxy.
func TestProxyFlag_EveryHostUsesProxy(t *testing.T) {
	captureLogs(t)
	proxy := fakeapi.NewProxy(t)
	dir := t.TempDir()
	config := filepath.Join(dir, "provider-config.yaml")
	keys := "breachdirectory: [k]\nbreachhouse: [k]\ndehashed: [k]\nhibr: [k]\nhudsonrock: [k]\n" +
		"intelx: ['free.intelx.io:k']\nleakcheck: [k]\nleakradar: [k]\nleaklookup: [k]\nleaksight: [k]\n" +
		"osintleak: [k]\nsnusbase: [k]\nweleakinfo: [k]\nwhiteintel: [k]\n"
	if err := os.WriteFile(config, []byte(keys), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := NewRunner(&Options{
		ProviderConfig: config, DBPath: filepath.Join(dir, "leaker.db"), NoWriteDB: true,
		Sources: []string{"online"}, Proxy: proxy.URL, Verify: true, NoRateLimit: true, Timeout: 5 * time.Second,
		Output: &bytes.Buffer{}, Type: sources.TypeDomain, // every source can search a domain
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	// The proxy refuses everything, so every source fails.
	if err := r.EnumerateSingleTarget(context.Background(), "example.com", sources.TypeDomain, []io.Writer{&bytes.Buffer{}}); !errors.Is(err, errAllSourcesFailed) {
		t.Fatalf("err = %v, want errAllSourcesFailed", err)
	}
	r.verifier.EnrichResult(context.Background(), &sources.Result{Password: "password"})

	hosts := proxy.Hosts()
	for _, want := range []string{
		"breachdirectory.p.rapidapi.com", "breach.house", "api.dehashed.com", "haveibeenransom.com",
		"api.hudsonrock.com", "free.intelx.io", "leakcheck.io", "api.leakradar.io", "leak-lookup.com",
		"api.leaksight.com", "osintleak.com", "api.proxynova.com", "api.snusbase.com", "api.weleakinfo.io",
		"api.whiteintel.io", "api.pwnedpasswords.com",
	} {
		if !slices.Contains(hosts, want) {
			t.Errorf("%s did not go through the proxy (proxy saw %v)", want, hosts)
		}
	}
}

func TestNewRunner_InvalidProxyIsConfigError(t *testing.T) {
	captureLogs(t)
	config := writeProviderConfig(t, "")
	_, err := NewRunner(&Options{ProviderConfig: config, Sources: []string{"proxynova"}, Proxy: "127.0.0.1:8080",
		NoWriteDB: true, DBPath: filepath.Join(t.TempDir(), "x.db")})
	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v, want *ConfigError", err)
	}
}

func requestPaths(reqs []fakeapi.Request) []string {
	var paths []string
	for _, r := range reqs {
		paths = append(paths, r.Method+" "+r.URL.Path)
	}
	return paths
}
