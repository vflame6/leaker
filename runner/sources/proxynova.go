package sources

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type ProxyNovaResponse struct {
	Count int      `json:"count"`
	Lines []string `json:"lines"`
}

type ProxyNova struct {
}

// Run function returns all subdomains found with the service
func (s *ProxyNova) Run(email string) <-chan Result {
	results := make(chan Result)

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // This is the key field to ignore certificate errors
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	client := &http.Client{
		Transport: transport,
	}

	go func() {
		var response ProxyNovaResponse

		defer func() {
			close(results)
		}()

		resp, err := client.Get(fmt.Sprintf("https://api.proxynova.com/comb?query=%s&start=0&limit=100", email))
		if err != nil {
			results <- Result{
				Source: s.Name(),
				Value:  "",
				Error:  err,
			}
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			results <- Result{
				Source: s.Name(),
				Value:  "",
				Error:  err,
			}
			return
		}

		err = json.Unmarshal(body, &response)
		if err != nil {
			results <- Result{
				Source: s.Name(),
				Value:  "",
				Error:  err,
			}
			return
		}

		if response.Count > 0 {
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

func (s *ProxyNova) GetRateLimit() int {
	return 90
}
