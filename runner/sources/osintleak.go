package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
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
			searchType = "url"
		case TypeKeyword:
			searchType = "password"
		case TypePhone:
			searchType = "phone"
		}

		url := fmt.Sprintf(
			"https://osintleak.com/api/v1/search_api/?api_key=%s&query=%s&type=%s&stealerlogs=true&dbleaks=true&dbleaks2=true&page=1&page_size=100",
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

			r := Result{Source: s.Name()}
			if val, ok := entry["email"].(string); ok && val != "" {
				r.Email = val
			}
			if val, ok := entry["username"].(string); ok && val != "" {
				r.Username = val
			}
			if val, ok := entry["password"].(string); ok && val != "" {
				r.Password = val
			}
			if val, ok := entry["phone"].(string); ok && val != "" {
				r.Phone = val
			}
			if val, ok := entry["name"].(string); ok && val != "" {
				r.Name = val
			}
			if val, ok := entry["ip"].(string); ok && val != "" {
				r.IP = val
			}
			if val, ok := entry["url"].(string); ok && val != "" {
				r.URL = val
			}
			if r.HasData() {
				results <- r
			}
		}
	}()

	return results
}

func (s *OSINTLeak) Name() string {
	return "osintleak"
}

func (s *OSINTLeak) UsesKey() bool {
	return true
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
