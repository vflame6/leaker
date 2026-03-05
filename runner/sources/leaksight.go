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

type LeakSight struct {
	apiKeys []string
}

func (s *LeakSight) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
		if randomApiKey == "" {
			return
		}

		// Pick endpoint based on scan type
		var endpoint string
		switch scanType {
		case TypeEmail:
			endpoint = "username" // searches by email/username
		case TypeUsername:
			endpoint = "username"
		case TypeDomain:
			endpoint = "url" // searches by URL/domain
		case TypeKeyword:
			endpoint = "password"
		case TypePhone:
			endpoint = "number"
		}

		url := fmt.Sprintf("https://api.leaksight.com/osint/%s?token=%s&text=%s",
			endpoint, randomApiKey, target)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in LeakSight source for %s", target)
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
		logger.Debugf("Response from LeakSight source: status code [%d], size [%d]", resp.StatusCode, len(body))

		if resp.StatusCode != http.StatusOK {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("LeakSight returned status %d: %s", resp.StatusCode, string(body)),
			}
			return
		}

		// Response format varies by endpoint. Try parsing as generic JSON.
		var response map[string]interface{}
		if err := json.Unmarshal(body, &response); err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		// Handle /osint/url response: {"total": N, "success": [{host, path, user, pass}, ...]}
		if successArr, ok := response["success"].([]interface{}); ok {
			for _, item := range successArr {
				entry, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				r := Result{Source: s.Name()}
				if val, ok := entry["user"].(string); ok && val != "" {
					r.Username = val
				}
				if val, ok := entry["pass"].(string); ok && val != "" {
					r.Password = val
				}
				if host, ok := entry["host"].(string); ok && host != "" {
					path, _ := entry["path"].(string)
					if path != "" {
						r.URL = host + path
					} else {
						r.URL = host
					}
				}
				if r.HasData() {
					results <- r
				}
			}
			return
		}

		// Handle /osint/username response: {stealer_json: [], database_url: [], bigcomboCombolist: []}
		for _, key := range []string{"stealer_json", "database_url", "bigcomboCombolist"} {
			arr, ok := response[key].([]interface{})
			if !ok || len(arr) == 0 {
				continue
			}
			for _, item := range arr {
				switch v := item.(type) {
				case map[string]interface{}:
					r := Result{Source: s.Name()}
					r.SetExtra("leak_type", key)
					if val, ok := v["email"].(string); ok && val != "" {
						r.Email = val
					}
					if val, ok := v["username"].(string); ok && val != "" {
						r.Username = val
					} else if val, ok := v["user"].(string); ok && val != "" {
						r.Username = val
					}
					if val, ok := v["password"].(string); ok && val != "" {
						r.Password = val
					} else if val, ok := v["pass"].(string); ok && val != "" {
						r.Password = val
					}
					if val, ok := v["host"].(string); ok && val != "" {
						r.URL = val
					} else if val, ok := v["url"].(string); ok && val != "" {
						r.URL = val
					}
					if r.HasData() {
						results <- r
					}
				case string:
					if v != "" {
						r := Result{Source: s.Name()}
						r.SetExtra(key, v)
						results <- r
					}
				}
			}
		}
	}()

	return results
}

func (s *LeakSight) Name() string {
	return "leaksight"
}

func (s *LeakSight) UsesKey() bool {
	return true
}

func (s *LeakSight) NeedsKey() bool {
	return true
}

func (s *LeakSight) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

func (s *LeakSight) RateLimit() int {
	return 2
}
