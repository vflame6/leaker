package sources

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

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

func (s *OSINTLeak) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if randomApiKey == "" {
		return newStats(), nil
	}

	var searchType string
	switch q.Type {
	case TypeEmail:
		searchType = "email"
	case TypeUsername:
		searchType = "username"
	case TypeDomain:
		searchType = "url"
	case TypePhone:
		searchType = "phone"
	default:
		return newStats(), unsupportedType(s, q.Type)
	}

	p := newPager(q, s.Limits())
	for page := 1; ; page++ {
		answer, err := s.fetchPage(ctx, session, randomApiKey, q.Target, searchType, page, p.size())
		if err != nil {
			return p.stats(), err
		}
		for _, r := range answer.records[:p.take(len(answer.records))] {
			if r.HasData() {
				emit(r)
			}
		}
		more := len(answer.records) == p.size()
		if answer.pages > 0 {
			more = page < answer.pages
		}
		if !p.next(len(answer.records), answer.count, more) {
			break
		}
	}
	return p.stats(), nil
}

// osintLeakPage is one page of results: count is the number of results the
// API reports (-1 if it does not), pages the number of pages (0 if unknown).
type osintLeakPage struct {
	records []Result
	count   int
	pages   int
}

// fetchPage reads one page of size results.
func (s *OSINTLeak) fetchPage(ctx context.Context, session *Session, apiKey, target, searchType string, page, size int) (osintLeakPage, error) {
	answer := osintLeakPage{count: -1}
	// The API only takes the key in the query string; Session.Do keeps
	// it out of errors and logs.
	query := url.Values{}
	query.Set("api_key", apiKey)
	query.Set("query", target)
	query.Set("type", searchType)
	query.Set("stealerlogs", "true")
	query.Set("dbleaks", "true")
	query.Set("dbleaks2", "true")
	query.Set("page", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(size))
	endpoint := "https://osintleak.com/api/v1/search_api/?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return answer, err
	}
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in OSINTLeak source for %s (page %d)", target, page)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return answer, err
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return answer, err
	}
	logger.Debugf("Response from OSINTLeak source: status code [%d], size [%d]", resp.StatusCode, len(body))

	var response map[string]interface{}
	if err := json.Unmarshal(body, &response); err != nil {
		return answer, err
	}
	if count, ok := response["count"].(float64); ok {
		answer.count = int(count)
	}
	if pages, ok := response["total_page"].(float64); ok {
		answer.pages = int(pages)
	}

	// The documented key is "results"; older answers used "data".
	data, ok := response["data"].([]interface{})
	if !ok {
		data, ok = response["results"].([]interface{})
		if !ok {
			return answer, nil
		}
	}

	records := make([]Result, 0, len(data))
	for _, item := range data {
		entry, ok := item.(map[string]interface{})
		if !ok {
			records = append(records, Result{})
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

		records = append(records, r)
	}
	answer.records = records
	return answer, nil
}

// Limits: 100 results per page.
func (s *OSINTLeak) Limits() Limits { return Limits{PageSize: 100} }

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

// Types: OSINTLeak has no keyword search.
func (s *OSINTLeak) Types() []ScanType {
	return []ScanType{TypeEmail, TypeUsername, TypeDomain, TypePhone}
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

// HasKeys reports whether a usable API key is configured.
func (s *OSINTLeak) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval returns the minimum time between two requests to the source.
func (s *OSINTLeak) MinInterval() time.Duration {
	return 500 * time.Millisecond
}
