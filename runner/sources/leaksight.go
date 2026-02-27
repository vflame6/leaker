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

type LeakSight struct {
	apiKeys []string
}

func (s *LeakSight) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name())
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
				var parts []string
				for _, field := range []string{"host", "user", "pass", "path"} {
					if val, ok := entry[field].(string); ok && val != "" {
						parts = append(parts, field+":"+val)
					}
				}
				if len(parts) > 0 {
					results <- Result{Source: s.Name(), Value: strings.Join(parts, ", ")}
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
					var parts []string
					parts = append(parts, "source:"+key)
					for _, field := range []string{"email", "username", "user", "password", "pass", "host", "url"} {
						if val, ok := v[field].(string); ok && val != "" {
							parts = append(parts, field+":"+val)
						}
					}
					if len(parts) > 1 {
						results <- Result{Source: s.Name(), Value: strings.Join(parts, ", ")}
					}
				case string:
					if v != "" {
						results <- Result{Source: s.Name(), Value: key + ":" + v}
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

func (s *LeakSight) IsDefault() bool {
	return false
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
