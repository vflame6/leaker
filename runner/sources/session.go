package sources

import (
	"crypto/tls"
	"fmt"
	"github.com/vflame6/leaker/logger"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// NewSession creates a new session object for an email.
// Set insecure=true only when the caller explicitly opts in via --insecure;
// by default TLS certificates are verified.
func NewSession(timeout time.Duration, userAgent, proxy string, insecure bool) (*Session, error) {
	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure, //nolint:gosec // controlled by --insecure flag
		},
		Dial: (&net.Dialer{
			Timeout: timeout,
		}).Dial,
	}

	// Add proxy
	// will raise an error if could not validate to ensure user's privacy
	if proxy != "" {
		proxyURL, _ := url.Parse(proxy)
		if proxyURL == nil {
			return nil, fmt.Errorf("invalid proxy provided: %s", proxy)
		} else {
			tr.Proxy = http.ProxyURL(proxyURL)
		}
	}

	customTransport := &CustomTransport{
		Transport: tr,
		UserAgent: userAgent,
	}

	client := &http.Client{
		Transport: customTransport,
		Timeout:   timeout,
	}

	session := &Session{Client: client}

	return session, nil
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
