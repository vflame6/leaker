package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vflame6/leaker/logger"
	"io"
)

type ProxyNovaResponse struct {
	Count int      `json:"count"`
	Lines []string `json:"lines"`
}

type ProxyNova struct {
}

// Run function returns all subdomains found with the service
func (s *ProxyNova) Run(email string, scanType ScanType, session *Session) <-chan Result {
	// ignore scanType because ProxyNova API works with any input
	// that's why result filtering is enabled by default

	results := make(chan Result)

	go func() {
		var response ProxyNovaResponse

		defer func() {
			close(results)
		}()

		logger.Debugf("Sending a request in ProxyNova source for %s", email)
		resp, err := session.Client.Get(fmt.Sprintf("https://api.proxynova.com/comb?query=%s&start=0&limit=100", email))
		if err != nil {
			results <- Result{
				Source: s.Name(),
				Value:  "",
				Error:  err,
			}
			return
		}
		defer session.DiscardHTTPResponse(resp)

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			results <- Result{
				Source: s.Name(),
				Value:  "",
				Error:  err,
			}
			return
		}
		logger.Debugf("Response from ProxyNova source: status code [%d], size [%d]", resp.StatusCode, len(body))

		err = json.Unmarshal(body, &response)
		if err != nil {
			results <- Result{
				Source: s.Name(),
				Value:  "",
				Error:  errors.New(fmt.Sprintf("failed to parse ProxyNova response: %s", string(body))),
			}
			return
		}

		if response.Count > 0 {
			// ProxyNova gives non-filtered results, so the output will be a mess
			for _, line := range response.Lines {
				results <- Result{
					Source: s.Name(),
					Value:  line,
					Error:  nil,
				}
			}
		}
	}()

	return results
}

// Name returns the name of the source
func (s *ProxyNova) Name() string {
	return "proxynova"
}

func (s *ProxyNova) IsDefault() bool {
	return true
}

func (s *ProxyNova) NeedsKey() bool {
	return false
}

func (s *ProxyNova) AddApiKeys(_ []string) {
	// no key needed
}

func (s *ProxyNova) RateLimit() int {
	// from https://www.proxynova.com/tools/comb/
	// "You are limited to about 100 requests per minute."
	return 90
}
