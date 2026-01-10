package sources

import (
	"crypto/tls"
	"github.com/vflame6/leaker/logger"
	"io"
	"net"
	"net/http"
	"time"
)

// NewSession creates a new session object for an email
func NewSession(timeout time.Duration) (*Session, error) {
	Transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		Dial: (&net.Dialer{
			Timeout: timeout,
		}).Dial,
	}

	client := &http.Client{
		Transport: Transport,
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
