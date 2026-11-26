// Package fakeapi provides network fakes for testing leaker without reaching
// any real service.
//
// A Server answers every request sent through its Transport, whatever host the
// client asked for, and records each request as the client sent it. A Proxy is
// a forward proxy that records the host of every request and refuses it.
package fakeapi

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// Request is one request received by a Server, as the client sent it.
type Request struct {
	Method string
	// URL is the URL the client asked for, e.g.
	// https://api.proxynova.com/comb?query=x, with its encoding untouched.
	URL    *url.URL
	Header http.Header
	Body   []byte
}

// Server is a fake API server. Its handler sees r.Host set to the host the
// client asked for, and r.URL with the path and query exactly as sent.
type Server struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []Request
}

// NewServer starts a Server that is closed when the test ends.
func NewServer(tb testing.TB, handler http.Handler) *Server {
	tb.Helper()
	s := &Server{srv: httptest.NewServer(handler)}
	tb.Cleanup(s.srv.Close)
	return s
}

// Transport returns a RoundTripper that delivers every request to the server.
func (s *Server) Transport() http.RoundTripper {
	return &redirectTransport{server: s}
}

// Requests returns the requests received so far, oldest first.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) record(r Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r)
}

type redirectTransport struct {
	server *Server
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	original := *req.URL
	t.server.record(Request{Method: req.Method, URL: &original, Header: req.Header.Clone(), Body: body})

	serverURL, err := url.Parse(t.server.srv.URL)
	if err != nil {
		return nil, err
	}
	out := req.Clone(req.Context())
	out.URL.Scheme = serverURL.Scheme
	out.URL.Host = serverURL.Host
	out.Host = req.URL.Host
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	return t.server.srv.Client().Transport.RoundTrip(out)
}

// FailingTransport fails every request with Err, like a proxy that is down.
type FailingTransport struct {
	Err error
}

// RoundTrip implements http.RoundTripper.
func (f FailingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return nil, f.Err
}

// Proxy is a forward proxy that records the host of every request it receives
// (CONNECT tunnels and plain HTTP alike) and refuses it with 403 Forbidden.
type Proxy struct {
	// URL is the proxy address, e.g. http://127.0.0.1:41234.
	URL string

	mu    sync.Mutex
	hosts []string
}

// NewProxy starts a Proxy that is closed when the test ends.
func NewProxy(tb testing.TB) *Proxy {
	tb.Helper()
	p := &Proxy{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if r.Method != http.MethodConnect && r.URL.Host != "" {
			host = r.URL.Host
		}
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		p.mu.Lock()
		p.hosts = append(p.hosts, host)
		p.mu.Unlock()
		http.Error(w, "fakeapi proxy refuses every request", http.StatusForbidden)
	}))
	tb.Cleanup(srv.Close)
	p.URL = srv.URL
	return p
}

// Hosts returns the host names requested through the proxy, oldest first.
func (p *Proxy) Hosts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.hosts...)
}
