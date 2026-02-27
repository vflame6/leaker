package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type OSINTLeak struct {
	apiKeys []string
}

func (s *OSINTLeak) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name())
		if randomApiKey == "" {
			return
		}

		var searchType string
		switch scanType {
		case TypeEmail:
			searchType = "email"
		case TypeUsername:
			searchType = "username"
		case TypeDomain:
			// OSINTLeak doesn't have a direct domain type; use email as closest match
			searchType = "email"
		case TypeKeyword:
			searchType = "password"
		case TypePhone:
			searchType = "phone"
		}

		url := fmt.Sprintf(
			"https://osintleak.com/search_api/?api_key=%s&query=%s&type=%s&stealerlogs=true&dbleaks=true&dbleaks2=true&page=1&page_size=100",
			randomApiKey, target, searchType,
		)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in OSINTLeak source for %s", target)
		resp, err := session.Client.Do(req)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		defer session.DiscardHTTPResponse(resp)

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		logger.Debugf("Response from OSINTLeak source: status code [%d], size [%d]", resp.StatusCode, len(body))

		if resp.StatusCode != http.StatusOK {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("OSINTLeak returned status %d: %s", resp.StatusCode, string(body)),
			}
			return
		}

		var response map[string]interface{}
		if err := json.Unmarshal(body, &response); err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		// Parse results array
		data, ok := response["data"].([]interface{})
		if !ok {
			// Try "results" key as fallback
			data, ok = response["results"].([]interface{})
			if !ok {
				// No results found or unexpected format
				return
			}
		}

		for _, item := range data {
			entry, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			var parts []string
			for _, field := range []string{"email", "username", "password", "phone", "name", "ip", "url"} {
				if val, ok := entry[field].(string); ok && val != "" {
					parts = append(parts, field+":"+val)
				}
			}

			if len(parts) > 0 {
				results <- Result{
					Source: s.Name(),
					Value:  strings.Join(parts, ", "),
				}
			}
		}
	}()

	return results
}

func (s *OSINTLeak) Name() string {
	return "osintleak"
}

func (s *OSINTLeak) IsDefault() bool {
	return false
}

func (s *OSINTLeak) NeedsKey() bool {
	return true
}

func (s *OSINTLeak) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

func (s *OSINTLeak) RateLimit() int {
	return 2
}
