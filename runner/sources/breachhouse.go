package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BreachHouse queries Breach House (breach.house), one of the two DarkEye APIs:
// ransomware victims, data breaches, leads and stealer records matching a domain.
// Shared DarkEye code is in darkeye.go.
type BreachHouse struct {
	apiKeys []string

	// baseURL overrides the API root (tests only).
	baseURL string
}

const (
	breachHouseBaseURL = "https://breach.house/api/v1"
	breachHouseName    = "Breach House"

	// Max results per category for one search (the API maximum).
	breachHouseMaxLimit = 200
)

// Breach House returns one list per category in this order.
var breachHouseCategories = []string{"ransomware", "breaches", "leads", "stealers"}

func (s *BreachHouse) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	return darkEyeSearch(ctx, darkEyeLookup{
		source:  s.Name(),
		product: breachHouseName,
		keys:    s.apiKeys,
		fetch: func(ctx context.Context, q Query, apiKey, domain string) ([]darkEyeRecord, Stats, error) {
			return s.fetch(ctx, session, q, apiKey, domain)
		},
	}, q, emit)
}

// fetch runs one cross-source search.
func (s *BreachHouse) fetch(ctx context.Context, session *Session, q Query, apiKey, domain string) ([]darkEyeRecord, Stats, error) {
	stats := newStats()
	// The limit applies to each category; the search has no further page.
	limit := newPager(q, s.Limits()).size()
	values := url.Values{}
	values.Set("q", domain)
	values.Set("limit", strconv.Itoa(limit))
	endpoint := s.root() + "/search?" + values.Encode()

	body, err := darkEyeGet(ctx, session, breachHouseName, endpoint, apiKey, s.apiKeys)
	if err != nil {
		return nil, stats, err
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil || data == nil {
		return nil, stats, fmt.Errorf("%s returned an invalid response", breachHouseName)
	}

	var records []darkEyeRecord
	for _, category := range breachHouseCategories {
		entries := breachHouseEntries(data[category])
		if len(entries) >= limit {
			// A full category may have more.
			stats.Truncated, stats.Limit = true, LimitResults
			if limit == breachHouseMaxLimit {
				stats.Limit = LimitAPI
			}
		}
		for _, entry := range entries {
			var item darkEyeItem
			if err := json.Unmarshal(entry, &item); err != nil {
				continue
			}
			id := strings.TrimSpace(string(item.VictimID))
			if id == "" {
				id = strings.TrimSpace(string(item.ID))
			}
			records = append(records, darkEyeRecord{category: category, id: id, item: item})
		}
	}
	return records, stats, nil
}

// breachHouseEntries returns the items of one category, which is either a plain
// list or an object with a "results" list.
func breachHouseEntries(raw json.RawMessage) []json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var wrapped struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return wrapped.Results
	}
	return nil
}

func (s *BreachHouse) root() string {
	if s.baseURL != "" {
		return strings.TrimRight(s.baseURL, "/")
	}
	return breachHouseBaseURL
}

// Types: Breach House searches by domain only.
func (s *BreachHouse) Types() []ScanType { return []ScanType{TypeDomain} }

// Limits: up to 200 records per category (documented maximum).
func (s *BreachHouse) Limits() Limits {
	return Limits{PageSize: breachHouseMaxLimit, MaxResults: breachHouseMaxLimit}
}

func (s *BreachHouse) Name() string {
	return "breachhouse"
}

func (s *BreachHouse) UsesKey() bool {
	return true
}

func (s *BreachHouse) NeedsKey() bool {
	return true
}

func (s *BreachHouse) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// HasKeys reports whether a usable API key is configured.
func (s *BreachHouse) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval is a conservative default (one request per second), not a documented limit.
func (s *BreachHouse) MinInterval() time.Duration {
	return time.Second
}
