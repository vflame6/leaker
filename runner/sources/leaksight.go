package sources

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type LeakSight struct {
	apiKeys []string
}

func (s *LeakSight) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	target, scanType := q.Target, q.Type
	stats := newStats()

	randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if randomApiKey == "" {
		return stats, nil
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
	case TypePhone:
		endpoint = "number"
	default:
		return stats, unsupportedType(s, scanType)
	}

	// The API only takes the token in the query string; Session.Do
	// keeps it out of errors and logs.
	query := url.Values{}
	query.Set("token", randomApiKey)
	query.Set("text", target)
	requestURL := "https://api.leaksight.com/osint/" + endpoint + "?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
	if err != nil {
		return stats, err
	}
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in LeakSight source for %s", target)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return stats, err
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return stats, err
	}
	logger.Debugf("Response from LeakSight source: status code [%d], size [%d]", resp.StatusCode, len(body))

	// Response format varies by endpoint. Try parsing as generic JSON.
	var response map[string]interface{}
	if err := json.Unmarshal(body, &response); err != nil {
		return stats, err
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
				emit(r)
			}
		}
		return stats, nil
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
					emit(r)
				}
			case string:
				if v != "" {
					r := Result{Source: s.Name()}
					r.SetExtra(key, v)
					emit(r)
				}
			}
		}
	}
	return stats, nil
}

// Types: LeakSight has no keyword search.
func (s *LeakSight) Types() []ScanType {
	return []ScanType{TypeEmail, TypeUsername, TypeDomain, TypePhone}
}

// Limits: one answer holds every record.
func (s *LeakSight) Limits() Limits { return Limits{} }

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

// HasKeys reports whether a usable API key is configured.
func (s *LeakSight) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval returns the minimum time between two requests to the source.
func (s *LeakSight) MinInterval() time.Duration {
	return 500 * time.Millisecond
}
