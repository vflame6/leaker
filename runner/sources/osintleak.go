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

// osintleakIgnoredFields are internal/metadata fields that should not appear in results.
var osintleakIgnoredFields = map[string]struct{}{
	// Internal/metadata fields
	"id":            {},
	"ds":            {},
	"log_info":      {},
	"leak_id":       {},
	"uuid":          {},
	"log_name":      {}, // handled separately → Database
	"log_timestamp": {}, // internal timestamp
	// Primary fields handled explicitly
	"email":      {},
	"username":   {},
	"password":   {},
	"password2":  {},
	"phone":      {},
	"name":       {},
	"first_name": {},
	"last_name":  {},
	"ip":         {},
	"ip_address": {},
	"url":        {},
	"pass_hash":  {},
	"pass_salt":  {},
	// Geo/network metadata (noise in output)
	"country":        {},
	"country_name":   {},
	"continent":      {},
	"continent_name": {},
	"asn":            {},
	"as_name":        {},
	"as_domain":      {},
	// d2 dataset internal fields
	"user_id":     {},
	"joined_date": {},
	"fb_id":       {},
	"email2":      {},
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
			searchType = "username"
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
				return
			}
		}

		for _, item := range data {
			entry, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			r := Result{Source: s.Name()}

			// Primary fields — extract with null/None handling
			r.Email = osintleakString(entry, "email")
			r.Username = osintleakString(entry, "username")
			r.Password = osintleakString(entry, "password")
			r.Phone = osintleakString(entry, "phone")
			r.Name = osintleakString(entry, "name")
			r.IP = osintleakString(entry, "ip")
			r.URL = osintleakString(entry, "url")
			r.Hash = osintleakString(entry, "pass_hash")
			r.Salt = osintleakString(entry, "pass_salt")

			// ip_address is used in d2 dataset entries
			if r.IP == "" {
				r.IP = osintleakString(entry, "ip_address")
			}

			// Map log_name → Database (can be null or missing)
			r.Database = osintleakString(entry, "log_name")

			// Handle first_name/last_name if present
			if fn := osintleakString(entry, "first_name"); fn != "" {
				if r.Name != "" {
					r.Name = fn + " " + r.Name
				} else {
					r.Name = fn
				}
			}
			if ln := osintleakString(entry, "last_name"); ln != "" {
				if r.Name != "" {
					r.Name += " " + ln
				} else {
					r.Name = ln
				}
			}

			// Extra fields — anything not in the ignored/primary set
			for key, val := range entry {
				if _, ignored := osintleakIgnoredFields[key]; ignored {
					continue
				}
				if key == "first_name" || key == "last_name" {
					continue
				}
				strVal := osintleakStringVal(val)
				if strVal == "" {
					continue
				}
				r.SetExtra(key, strVal)
			}

			if r.HasData() {
				results <- r
			}
		}
	}()

	return results
}

// osintleakString extracts a string value from a map entry, treating null and "None" as empty.
func osintleakString(entry map[string]interface{}, key string) string {
	val, exists := entry[key]
	if !exists || val == nil {
		return ""
	}
	s, ok := val.(string)
	if !ok || s == "None" {
		return ""
	}
	return s
}

// osintleakStringVal converts an interface{} to a string, treating null and "None" as empty.
func osintleakStringVal(val interface{}) string {
	if val == nil {
		return ""
	}
	s, ok := val.(string)
	if !ok {
		return ""
	}
	if s == "None" {
		return ""
	}
	return s
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
