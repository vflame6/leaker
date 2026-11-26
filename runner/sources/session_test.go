package sources

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
)

func TestRedactSecrets(t *testing.T) {
	cases := map[string]string{
		"https://api.leaksight.com/osint/username?token=SECRET&text=jane":   "https://api.leaksight.com/osint/username?token=REDACTED&text=jane",
		"https://osintleak.com/api/v1/search_api/?query=x&api_key=SECRET&p": "https://osintleak.com/api/v1/search_api/?query=x&api_key=REDACTED&p",
		"https://x.test/?apikey=SECRET":                                     "https://x.test/?apikey=REDACTED",
		"https://x.test/?KEY=SECRET#frag":                                   "https://x.test/?KEY=REDACTED#frag",
		"https://x.test/?monkey=banana&keyword=ok":                          "https://x.test/?monkey=banana&keyword=ok",
		`Get "https://x.test/?a=1&token=S3CR3T": proxy down`:                `Get "https://x.test/?a=1&token=REDACTED": proxy down`,
	}
	for in, want := range cases {
		if got := RedactSecrets(in); got != want {
			t.Errorf("RedactSecrets(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestSessionDo_RedactsKeyOnTransportError(t *testing.T) {
	sentinel := errors.New("proxy down")
	session, err := NewSession(SessionOptions{Timeout: time.Second, Transport: fakeapi.FailingTransport{Err: sentinel}})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.leaksight.com/osint/username?token=SECRET_LEAKSIGHT_TOKEN_456&text=jane", nil)

	_, err = session.Do(context.Background(), req)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "SECRET_LEAKSIGHT_TOKEN_456") {
		t.Errorf("error leaks the key: %v", err)
	}
	if !strings.Contains(err.Error(), "token=REDACTED") {
		t.Errorf("error should show the redacted parameter: %v", err)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("redaction must keep the error chain: %v", err)
	}
}

func TestNewSession_InvalidProxy(t *testing.T) {
	for _, proxy := range []string{"127.0.0.1:8080", "ftp://proxy.test:21", "http://", "http://user:hunter2@"} {
		_, err := NewSession(SessionOptions{Proxy: proxy})
		if err == nil {
			t.Errorf("NewSession(proxy %q) should fail", proxy)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("proxy error leaks the password: %v", err)
		}
	}
}

func TestNewSession_ProxyFlagRoutesRequests(t *testing.T) {
	proxy := fakeapi.NewProxy(t)
	session, err := NewSession(SessionOptions{Timeout: 5 * time.Second, Proxy: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.proxynova.com/comb", nil)
	if _, err := session.Do(context.Background(), req); err == nil {
		t.Error("the fake proxy refuses requests, expected an error")
	}
	if got := proxy.Hosts(); len(got) != 1 || got[0] != "api.proxynova.com" {
		t.Errorf("proxy saw %v, want [api.proxynova.com]", got)
	}
}

func TestNewSession_DefaultsToEnvironmentProxy(t *testing.T) {
	session, err := NewSession(SessionOptions{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	custom, ok := session.client.Transport.(*CustomTransport)
	if !ok {
		t.Fatalf("transport is %T, want *CustomTransport", session.client.Transport)
	}
	tr, ok := custom.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("inner transport is %T, want *http.Transport", custom.Transport)
	}
	if tr.Proxy == nil || reflect.ValueOf(tr.Proxy).Pointer() != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Error("without --proxy the session must use http.ProxyFromEnvironment")
	}
}

func TestMinIntervals(t *testing.T) {
	if got := (&ProxyNova{}).MinInterval(); got != 667*time.Millisecond {
		t.Errorf("ProxyNova.MinInterval() = %v, want 667ms (about 90 requests per minute)", got)
	}
	if got := (&LocalDB{}).MinInterval(); got != 0 {
		t.Errorf("LocalDB.MinInterval() = %v, want 0", got)
	}
	online := []Source{
		&BreachDirectory{}, &BreachHouse{}, &DeHashed{}, &HIBR{}, &HudsonRock{}, &IntelX{},
		&LeakCheck{}, &LeakLookup{}, &LeakRadar{}, &LeakSight{}, &OSINTLeak{}, &Snusbase{},
		&WeLeakInfo{}, &WhiteIntel{},
	}
	for _, s := range online {
		if s.MinInterval() <= 0 {
			t.Errorf("%s.MinInterval() = %v, want > 0", s.Name(), s.MinInterval())
		}
	}
}

func TestHasKeys(t *testing.T) {
	s := &Snusbase{}
	if s.HasKeys() {
		t.Error("new source should have no keys")
	}
	s.AddApiKeys([]string{"k"})
	if !s.HasKeys() {
		t.Error("source with a key should report HasKeys")
	}

	ix := &IntelX{}
	ix.AddApiKeys([]string{"not-a-host-key"})
	if ix.HasKeys() {
		t.Error("IntelX keys without HOST: prefix are unusable")
	}
	ix.AddApiKeys([]string{"free.intelx.io:uuid-1"})
	ix.AddApiKeys([]string{"free.intelx.io:uuid-2"})
	if len(ix.apiKeys) != 1 {
		t.Errorf("AddApiKeys should replace keys, have %d", len(ix.apiKeys))
	}
}

// TestNewSession_IdleConnectionsTimeOut: connections are kept alive, so idle
// ones are closed after 90s, like Go's default transport, before a server
// does it under a request.
func TestNewSession_IdleConnectionsTimeOut(t *testing.T) {
	session, err := NewSession(SessionOptions{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	tr := session.client.Transport.(*CustomTransport).Transport.(*http.Transport)
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", tr.IdleConnTimeout)
	}
}
