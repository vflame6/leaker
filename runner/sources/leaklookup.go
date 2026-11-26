package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

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

func (s *LeakLookup) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	target, scanType := q.Target, q.Type
	stats := newStats()

	randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if randomApiKey == "" {
		return stats, nil
	}

	var searchType string
	switch scanType {
	case TypeEmail:
		searchType = "email_address"
	case TypeUsername:
		searchType = "username"
	case TypeDomain:
		searchType = "domain"
	case TypePhone:
		searchType = "phone"
	default:
		return stats, unsupportedType(s, scanType)
	}

	form := url.Values{}
	form.Set("key", randomApiKey)
	form.Set("type", searchType)
	form.Set("query", target)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://leak-lookup.com/api/search",
		strings.NewReader(form.Encode()))
	if err != nil {
		return stats, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in LeakLookup source for %s", target)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return stats, err
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return stats, err
	}
	logger.Debugf("Response from LeakLookup source: status code [%d], size [%d]", resp.StatusCode, len(body))

	var response leakLookupResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return stats, err
	}

	if response.Error == "true" {
		var errMsg string
		_ = json.Unmarshal(response.Message, &errMsg)
		return stats, fmt.Errorf("LeakLookup error: %s", errorBody([]byte(errMsg), s.apiKeys...))
	}

	// Message is a map of breach_name -> []record
	// Private keys get full records; public keys get empty arrays
	var breaches map[string]json.RawMessage
	if err := json.Unmarshal(response.Message, &breaches); err != nil {
		// Might be a string message instead
		return stats, nil
	}

	// Map order is random; sort so the same response always prints the same way.
	for _, breachName := range slices.Sorted(maps.Keys(breaches)) {
		rawRecords := breaches[breachName]
		// Try to parse as array of objects (private key response)
		var records []map[string]interface{}
		if err := json.Unmarshal(rawRecords, &records); err != nil {
			// Public key: breach name only
			emit(Result{Source: s.Name(), Database: breachName})
			continue
		}

		if len(records) == 0 {
			// Public key: empty array means breach found but no details
			emit(Result{Source: s.Name(), Database: breachName})
			continue
		}

		for _, record := range records {
			r := Result{Source: s.Name(), Database: breachName}
			// LeakLookup uses both "email_address" and "email"
			if val, ok := record["email_address"].(string); ok && val != "" {
				r.Email = val
			}
			if val, ok := record["email"].(string); ok && val != "" {
				r.Email = val
			}
			if val, ok := record["username"].(string); ok && val != "" {
				r.Username = val
			}
			if val, ok := record["password"].(string); ok && val != "" {
				r.Password = val
			}
			if val, ok := record["hash"].(string); ok && val != "" {
				r.Hash = val
			}
			if val, ok := record["phone"].(string); ok && val != "" {
				r.Phone = val
			}
			if val, ok := record["ip_address"].(string); ok && val != "" {
				r.IP = val
			}
			if val, ok := record["fullname"].(string); ok && val != "" {
				r.Name = val
			}
			if r.HasData() {
				emit(r)
			}
		}
	}
	return stats, nil
}

// Types: LeakLookup has no keyword search.
func (s *LeakLookup) Types() []ScanType {
	return []ScanType{TypeEmail, TypeUsername, TypeDomain, TypePhone}
}

// Limits: one answer holds every record.
func (s *LeakLookup) Limits() Limits { return Limits{} }

func (s *LeakLookup) Name() string {
	return "leaklookup"
}

func (s *LeakLookup) UsesKey() bool {
	return true
}

func (s *LeakLookup) NeedsKey() bool {
	return true
}

func (s *LeakLookup) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// HasKeys reports whether a usable API key is configured.
func (s *LeakLookup) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval: public keys allow 10 requests per day.
func (s *LeakLookup) MinInterval() time.Duration {
	return time.Second
}
