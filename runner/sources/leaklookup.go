package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type LeakLookup struct {
	apiKeys []string
}

type leakLookupResponse struct {
	Error   string          `json:"error"`
	Message json.RawMessage `json:"message"`
}

func (s *LeakLookup) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
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
			searchType = "email_address"
		case TypeUsername:
			searchType = "username"
		case TypeDomain:
			searchType = "domain"
		case TypeKeyword:
			searchType = "password"
		}

		form := url.Values{}
		form.Set("key", randomApiKey)
		form.Set("type", searchType)
		form.Set("query", target)

		req, err := http.NewRequestWithContext(ctx, "POST", "https://leak-lookup.com/api/search",
			strings.NewReader(form.Encode()))
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in LeakLookup source for %s", target)
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
		logger.Debugf("Response from LeakLookup source: status code [%d], size [%d]", resp.StatusCode, len(body))

		if resp.StatusCode != http.StatusOK {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("LeakLookup returned status %d: %s", resp.StatusCode, string(body)),
			}
			return
		}

		var response leakLookupResponse
		if err := json.Unmarshal(body, &response); err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		if response.Error == "true" {
			var errMsg string
			_ = json.Unmarshal(response.Message, &errMsg)
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("LeakLookup error: %s", errMsg),
			}
			return
		}

		// Message is a map of breach_name -> []record
		// Private keys get full records; public keys get empty arrays
		var breaches map[string]json.RawMessage
		if err := json.Unmarshal(response.Message, &breaches); err != nil {
			// Might be a string message instead
			return
		}

		for breachName, rawRecords := range breaches {
			// Try to parse as array of objects (private key response)
			var records []map[string]interface{}
			if err := json.Unmarshal(rawRecords, &records); err != nil {
				// Public key: breach name only
				results <- Result{
					Source: s.Name(),
					Value:  "breach:" + breachName,
				}
				continue
			}

			if len(records) == 0 {
				// Public key: empty array means breach found but no details
				results <- Result{
					Source: s.Name(),
					Value:  "breach:" + breachName,
				}
				continue
			}

			for _, record := range records {
				var parts []string
				parts = append(parts, "breach:"+breachName)
				for _, field := range []string{"email_address", "email", "username", "password", "hash", "phone", "ip_address", "fullname"} {
					if val, ok := record[field].(string); ok && val != "" {
						parts = append(parts, field+":"+val)
					}
				}
				results <- Result{
					Source: s.Name(),
					Value:  strings.Join(parts, ", "),
				}
			}
		}
	}()

	return results
}

func (s *LeakLookup) Name() string {
	return "leaklookup"
}

func (s *LeakLookup) IsDefault() bool {
	return false
}

func (s *LeakLookup) NeedsKey() bool {
	return true
}

func (s *LeakLookup) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

func (s *LeakLookup) RateLimit() int {
	// Public: 10 requests/day
	return 1
}
