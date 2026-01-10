package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type ProxyNovaResponse struct {
	Count int      `json:"count"`
	Lines []string `json:"lines"`
}

type ProxyNova struct {
}

// Run function returns all subdomains found with the service
func (s *ProxyNova) Run(email string, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		var response ProxyNovaResponse

		defer func() {
			close(results)
		}()

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
			// ProxyNova gives non-filtered results, so check if the result contains a username
			username := strings.Split(email, "@")[0]
			for _, line := range response.Lines {
				if strings.Contains(line, username) {
					results <- Result{
						Source: s.Name(),
						Value:  line,
						Error:  nil,
					}
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
