package fakeapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestServer_AnswersEveryHostAndRecordsRequests(t *testing.T) {
	var seenHost, seenQuery string
	srv := NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		seenQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, "ok")
	}))
	client := &http.Client{Transport: srv.Transport()}

	resp, err := client.Post("https://api.example.com/search?q=a%2Bb", "text/plain", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
	if seenHost != "api.example.com" || seenQuery != "q=a%2Bb" {
		t.Errorf("handler saw host %q query %q", seenHost, seenQuery)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(reqs))
	}
	if got := reqs[0].URL.String(); got != "https://api.example.com/search?q=a%2Bb" {
		t.Errorf("recorded URL %q", got)
	}
	if string(reqs[0].Body) != "payload" || reqs[0].Method != http.MethodPost {
		t.Errorf("recorded %s body %q", reqs[0].Method, reqs[0].Body)
	}
}

func TestFailingTransport_ReturnsItsError(t *testing.T) {
	sentinel := errors.New("proxy down")
	client := &http.Client{Transport: FailingTransport{Err: sentinel}}
	_, err := client.Get("https://api.example.com/")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped sentinel", err)
	}
}

func TestProxy_RecordsHostsAndRefuses(t *testing.T) {
	proxy := NewProxy(t)
	proxyURL, _ := url.Parse(proxy.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	if _, err := client.Get("https://secure.example.com/path"); err == nil {
		t.Error("CONNECT through the fake proxy should fail")
	}
	resp, err := client.Get("http://plain.example.com/path")
	if err != nil {
		t.Fatalf("plain request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}

	got := proxy.Hosts()
	want := []string{"secure.example.com", "plain.example.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("hosts = %v, want %v", got, want)
	}
}
