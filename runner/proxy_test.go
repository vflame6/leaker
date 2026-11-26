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
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
	"github.com/vflame6/leaker/runner/sources"
)

// testSession returns a session on transport with fast retries.
func testSession(t *testing.T, transport http.RoundTripper) *sources.Session {
	t.Helper()
	session, err := sources.NewSession(sources.SessionOptions{Timeout: 5 * time.Second, UserAgent: "leaker-test", Transport: transport,
		Retry: sources.RetryPolicy{BaseDelay: time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// TestProxyFlag_EveryHostUsesProxy: with --proxy, every source goes through
// the proxy.
func TestProxyFlag_EveryHostUsesProxy(t *testing.T) {
	captureLogs(t)
	captureSummary(t)
	proxy := fakeapi.NewProxy(t)
	dir := t.TempDir()
	config := filepath.Join(dir, "provider-config.yaml")
	keys := "breachdirectory: [k]\nbreachhouse: [k]\ndehashed: [k]\nhibr: [k]\nhudsonrock: [k]\n" +
		"intelx: ['free.intelx.io:k']\nleakcheck: [k]\nleakradar: [k]\nleaklookup: [k]\nleaksight: [k]\n" +
		"osintleak: [k]\nsnusbase: [k]\nweleakinfo: [k]\nwhiteintel: [k]\nspycloud: [k]\nflare: [k]\n"
	if err := os.WriteFile(config, []byte(keys), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := NewRunner(&Options{
		ProviderConfig: config, DBPath: filepath.Join(dir, "leaker.db"), NoWriteDB: true,
		Sources: []string{"online"}, Proxy: proxy.URL, NoRateLimit: true, Timeout: 5 * time.Second,
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

	hosts := proxy.Hosts()
	for _, want := range []string{
		"breachdirectory.p.rapidapi.com", "breach.house", "api.dehashed.com", "haveibeenransom.com",
		"api.hudsonrock.com", "free.intelx.io", "leakcheck.io", "api.leakradar.io", "leak-lookup.com",
		"api.leaksight.com", "osintleak.com", "api.proxynova.com", "api.snusbase.com", "api.weleakinfo.io",
		"api.whiteintel.io", "api.spycloud.io", "api.flare.io",
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
