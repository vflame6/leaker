package sources

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vflame6/leaker/logger"
)

// SessionOptions configures NewSession.
type SessionOptions struct {
	Timeout   time.Duration
	UserAgent string
	// Proxy is the --proxy URL. When empty, the HTTP_PROXY, HTTPS_PROXY and
	// NO_PROXY environment variables decide, as for any Go program.
	Proxy string
	// Insecure disables TLS certificate verification (--insecure).
	Insecure bool
	// Transport replaces the network transport, for tests. Proxy and Insecure
	// do not apply to it.
	Transport http.RoundTripper
}

// NewSession creates the HTTP session shared by every source during a run.
// An invalid proxy is an error, so traffic never silently bypasses it.
func NewSession(opts SessionOptions) (*Session, error) {
	transport := opts.Transport
	if transport == nil {
		tr := &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: opts.Insecure, //nolint:gosec // controlled by --insecure flag
			},
			DialContext: (&net.Dialer{
				Timeout: opts.Timeout,
			}).DialContext,
		}
		if opts.Proxy != "" {
			proxyURL, err := parseProxy(opts.Proxy)
			if err != nil {
				return nil, err
			}
			tr.Proxy = http.ProxyURL(proxyURL)
		}
		transport = tr
	}

	client := &http.Client{
		Transport: &CustomTransport{
			Transport: transport,
			UserAgent: opts.UserAgent,
		},
		Timeout: opts.Timeout,
	}
	return &Session{Client: client}, nil
}

// parseProxy validates a --proxy value. Error messages never include a
// password from the URL.
func parseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return nil, errors.New("invalid proxy: use a URL such as http://127.0.0.1:8080")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return u, nil
	default:
		return nil, fmt.Errorf("invalid proxy %s: the scheme must be http, https, socks5 or socks5h", u.Redacted())
	}
}

// secretParams matches query parameters that carry API keys.
var secretParams = regexp.MustCompile(`(?i)([?&](?:token|api_key|apikey|key)=)[^&#\s"']*`)

// RedactSecrets replaces the values of the token, api_key, apikey and key
// query parameters in s with REDACTED. s may be a URL or any text containing
// URLs, such as an error message.
func RedactSecrets(s string) string {
	return secretParams.ReplaceAllString(s, "${1}REDACTED")
}

const (
	// errorBodyLimit caps how much of an API error response an error quotes.
	errorBodyLimit = 300
	// minRedactedKeyLength skips replacing implausibly short "keys", which
	// would only garble the message.
	minRedactedKeyLength = 4
)

// errorBody prepares an API error response for an error message. Some
// services, and proxy block pages, echo the request back, so every given
// key is replaced with REDACTED wherever it appears, secret query parameters
// are redacted as in RedactSecrets, and the text is put on one line and cut
// to errorBodyLimit bytes.
func errorBody(body []byte, keys ...string) string {
	s := string(body)
	for _, key := range keys {
		if len(key) >= minRedactedKeyLength {
			s = strings.ReplaceAll(s, key, "REDACTED")
		}
	}
	s = strings.Join(strings.Fields(RedactSecrets(s)), " ")
	if len(s) > errorBodyLimit {
		cut := errorBodyLimit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}

// Do sends req with the session's client. Sources must send every request
// through Do: errors and debug logs never carry API keys from the URL.
func (s *Session) Do(req *http.Request) (*http.Response, error) {
	logger.Debugf("%s %s", req.Method, RedactSecrets(req.URL.Redacted()))
	resp, err := s.Client.Do(req)
	if err != nil {
		// http.Client puts the full request URL into its *url.Error.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			urlErr.URL = RedactSecrets(urlErr.URL)
		}
		return nil, err
	}
	return resp, nil
}

// DiscardHTTPResponse discards the response content by demand
func (s *Session) DiscardHTTPResponse(response *http.Response) {
	if response != nil {
		_, err := io.Copy(io.Discard, response.Body)
		if err != nil {
			logger.Errorf("Could not discard response body: %s\n", err)
			return
		}
		if closeErr := response.Body.Close(); closeErr != nil {
			logger.Errorf("Could not close response body: %s\n", closeErr)
		}
	}
}

// Close the session
func (s *Session) Close() {
	s.Client.CloseIdleConnections()
}

// RoundTrip implements the http.RoundTripper interface.
// custom one is needed to specify user agent string.
func (t *CustomTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Set the User-Agent header on the request.
	req.Header.Set("User-Agent", t.UserAgent)
	// set other headers
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Connection", "close")
	// Use the underlying transport to perform the actual request.
	return t.Transport.RoundTrip(req)
}
