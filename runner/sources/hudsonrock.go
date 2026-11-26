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

type HudsonRock struct {
	apiKeys []string
}

type hudsonRockFreeResponse struct {
	Stealers []map[string]interface{} `json:"stealers"`
}

type hudsonRockPaidResponse struct {
	Data []map[string]interface{} `json:"data"`
}

func (s *HudsonRock) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	if !SupportsScanType(s, q.Type) {
		return newStats(), unsupportedType(s, q.Type)
	}
	if apiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey()); apiKey != "" {
		// Paid Cavalier v3 API
		return s.searchPaid(ctx, q, session, emit, apiKey)
	}
	// Free OSINT endpoints
	return s.searchFree(ctx, q, session, emit)
}

func (s *HudsonRock) searchFree(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	const baseURL = "https://cavalier.hudsonrock.com/api/json/v2/osint-tools/"
	stats := newStats()

	query := url.Values{}
	var endpoint string
	switch q.Type {
	case TypeEmail:
		query.Set("email", q.Target)
		endpoint = baseURL + "search-by-email?" + query.Encode()
	case TypeDomain:
		query.Set("domain", q.Target)
		endpoint = baseURL + "search-by-domain?" + query.Encode()
	default:
		query.Set("username", q.Target)
		endpoint = baseURL + "search-by-username?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return stats, err
	}
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in HudsonRock (free) source for %s", q.Target)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return stats, err
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return stats, err
	}
	logger.Debugf("Response from HudsonRock (free) source: status code [%d], size [%d]", resp.StatusCode, len(body))

	var response hudsonRockFreeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return stats, err
	}
	for _, stealer := range response.Stealers {
		if r := s.recordToResult(stealer); r.HasData() {
			emit(r)
		}
	}
	return stats, nil
}

func (s *HudsonRock) searchPaid(ctx context.Context, q Query, session *Session, emit func(Result), apiKey string) (Stats, error) {
	const baseURL = "https://api.hudsonrock.com/json/v3/search-by-domain"
	stats := newStats()

	var searchType string
	switch q.Type {
	case TypeEmail:
		searchType = "email"
	case TypeDomain:
		searchType = "domain"
	default:
		searchType = "username"
	}

	query := url.Values{}
	query.Set("type", searchType)
	query.Set("query", q.Target)
	endpoint := baseURL + "?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return stats, err
	}
	req.Header.Set("api-key", apiKey)
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in HudsonRock (paid) source for %s", q.Target)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return stats, err
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return stats, err
	}
	logger.Debugf("Response from HudsonRock (paid) source: status code [%d], size [%d]", resp.StatusCode, len(body))

	var response hudsonRockPaidResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return stats, err
	}
	for _, record := range response.Data {
		if r := s.recordToResult(record); r.HasData() {
			emit(r)
		}
	}
	return stats, nil
}

func (s *HudsonRock) recordToResult(record map[string]interface{}) Result {
	r := Result{Source: s.Name()}
	if val, ok := record["email"].(string); ok && val != "" {
		r.Email = val
	}
	if val, ok := record["username"].(string); ok && val != "" {
		r.Username = val
	}
	if val, ok := record["password"].(string); ok && val != "" {
		r.Password = val
	}
	if val, ok := record["ip"].(string); ok && val != "" {
		r.IP = val
	}
	if val, ok := record["url"].(string); ok && val != "" {
		r.URL = val
	}
	if val, ok := record["computer_name"].(string); ok && val != "" {
		r.SetExtra("computer_name", val)
	}
	if val, ok := record["operating_system"].(string); ok && val != "" {
		r.SetExtra("operating_system", val)
	}
	if val, ok := record["date_compromised"].(string); ok && val != "" {
		r.SetExtra("date_compromised", val)
	}
	if val, ok := record["stealer_family"].(string); ok && val != "" {
		r.SetExtra("stealer_family", val)
	}
	return r
}

// Limits: one answer holds every record.
func (s *HudsonRock) Limits() Limits { return Limits{} }

func (s *HudsonRock) Name() string {
	return "hudsonrock"
}

func (s *HudsonRock) UsesKey() bool {
	return true
}

func (s *HudsonRock) NeedsKey() bool {
	// HudsonRock can work without an API key
	return false
}

func (s *HudsonRock) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// Types: Hudson Rock has no keyword or phone search.
func (s *HudsonRock) Types() []ScanType { return []ScanType{TypeEmail, TypeUsername, TypeDomain} }

// HasKeys reports whether a usable API key is configured.
func (s *HudsonRock) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval returns the minimum time between two requests to the source.
func (s *HudsonRock) MinInterval() time.Duration {
	return 100 * time.Millisecond
}
