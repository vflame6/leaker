package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type LeakCheck struct {
	apiKeys []string
}

// leakCheckMaxOffset is the largest offset the API accepts.
const leakCheckMaxOffset = 2500

// leakCheckPage is one answer of the query endpoint.
type leakCheckPage struct {
	records []Result
	found   int
	quota   *int
}

// Search pages through the records with limit and offset.
func (s *LeakCheck) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if randomApiKey == "" {
		return newStats(), nil
	}

	p := newPager(q, s.Limits())
	var quota *int
	for page := 1; ; page++ {
		offset := (page - 1) * p.size()
		answer, err := s.fetchPage(ctx, session, randomApiKey, q, p.size(), offset)
		if err != nil {
			stats := p.stats()
			stats.CreditsLeft = quota
			return stats, err
		}
		quota = answer.quota
		for _, r := range answer.records[:p.take(len(answer.records))] {
			emit(r)
		}
		// The next page starts at page*size, which the API must accept.
		more := page*p.size() <= leakCheckMaxOffset
		if !p.next(len(answer.records), answer.found, more) {
			break
		}
	}
	stats := p.stats()
	stats.CreditsLeft = quota
	return stats, nil
}

// fetchPage reads limit records from offset.
func (s *LeakCheck) fetchPage(ctx context.Context, session *Session, apiKey string, q Query, limit, offset int) (leakCheckPage, error) {
	var answer leakCheckPage
	var searchType string
	switch q.Type {
	case TypeEmail:
		searchType = "email"
	case TypeUsername:
		searchType = "username"
	case TypeDomain:
		searchType = "domain"
	case TypeKeyword:
		searchType = "keyword"
	case TypePhone:
		searchType = "phone"
	}
	query := url.Values{}
	query.Set("type", searchType)
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(offset))
	endpoint := "https://leakcheck.io/api/v2/query/" + url.PathEscape(q.Target) + "?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return answer, err
	}
	req.Header.Add("X-API-Key", apiKey)
	req.Header.Add("Accept", "application/json")

	logger.Debugf("Sending a request in LeakCheck source for %s (offset %d)", q.Target, offset)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return answer, leakCheckError(err)
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return answer, err
	}
	logger.Debugf("Response from LeakCheck source: status code [%d], size [%d]", resp.StatusCode, len(body))

	var response map[string]interface{}
	if err := json.Unmarshal(body, &response); err != nil {
		return answer, err
	}
	success, ok := response["success"].(bool)
	if !ok || !success {
		return answer, fmt.Errorf("failed to parse LeakCheck response: %s", errorBody(body, s.apiKeys...))
	}
	found, ok := response["found"].(float64)
	if !ok {
		return answer, fmt.Errorf("failed to parse LeakCheck response: %s", errorBody(body, s.apiKeys...))
	}
	answer.found = int(found)
	if quota, ok := response["quota"].(float64); ok {
		left := int(quota)
		answer.quota = &left
	}
	if answer.found == 0 {
		return answer, nil
	}
	jsonResults, ok := response["result"].([]interface{})
	if !ok {
		return answer, fmt.Errorf("failed to parse LeakCheck response: %s", errorBody(body, s.apiKeys...))
	}
	for _, jsonResult := range jsonResults {
		if r, ok := s.recordToResult(jsonResult); ok {
			answer.records = append(answer.records, r)
		}
	}
	return answer, nil
}

// recordToResult maps the listed fields of one record.
func (s *LeakCheck) recordToResult(record interface{}) (Result, bool) {
	parseResult, ok := record.(map[string]interface{})
	if !ok {
		return Result{}, false
	}
	jsonFields, ok := parseResult["fields"].([]interface{})
	if !ok {
		return Result{}, false
	}

	r := Result{Source: s.Name()}
	// Map source.name to Database (skip "Unknown" — it's their default for empty)
	if sourceObj, ok := parseResult["source"].(map[string]interface{}); ok {
		if sourceName, ok := sourceObj["name"].(string); ok && sourceName != "" && sourceName != "Unknown" {
			r.Database = sourceName
		}
	}
	for _, jsonField := range jsonFields {
		field, ok := jsonField.(string)
		if !ok {
			continue
		}
		val, ok := parseResult[field].(string)
		if !ok || val == "" {
			continue
		}
		switch field {
		case "email":
			r.Email = val
		case "username":
			r.Username = val
		case "password":
			r.Password = val
		case "hash":
			r.Hash = val
		case "ip":
			r.IP = val
		case "phone":
			r.Phone = val
		case "name", "first_name", "last_name":
			if r.Name != "" {
				r.Name += " " + val
			} else {
				r.Name = val
			}
		case "rdomain":
			// Ignored
		default:
			r.SetExtra(field, val)
		}
	}
	return r, r.HasData()
}

// leakCheckError gives LeakCheck's documented answers their kind: 400
// "Invalid API key" is a bad key and 403 "Limit reached" a spent quota.
func leakCheckError(err error) error {
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		return err
	}
	body := strings.ToLower(statusErr.Body)
	switch {
	case statusErr.StatusCode == http.StatusBadRequest && strings.Contains(body, "invalid api key"):
		return withKind(err, ErrAuth)
	case statusErr.StatusCode == http.StatusForbidden && strings.Contains(body, "limit reached"):
		return withKind(err, ErrQuota)
	}
	return err
}

// Limits: up to 1,000 records per request, from offsets up to 2,500.
func (s *LeakCheck) Limits() Limits { return Limits{PageSize: 1000} }

// Types: LeakCheck has a search type for each.
func (s *LeakCheck) Types() []ScanType { return AllTypes }

// Name returns the name of the source
func (s *LeakCheck) Name() string {
	return "leakcheck"
}

func (s *LeakCheck) UsesKey() bool {
	return true
}

func (s *LeakCheck) NeedsKey() bool {
	return true
}

func (s *LeakCheck) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// HasKeys reports whether a usable API key is configured.
func (s *LeakCheck) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval follows https://wiki.leakcheck.io/en/api/api-v2-pro:
// "By default, the LeakCheck Pro API is limited to 3 requests per second on any plan."
func (s *LeakCheck) MinInterval() time.Duration {
	return time.Second / 3
}
