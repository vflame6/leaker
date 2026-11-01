package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/vflame6/leaker/logger"
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

	// Max results per category for one search (the API maximum is 200).
	breachHouseLimit = 25
)

// Breach House returns one list per category in this order.
var breachHouseCategories = []string{"ransomware", "breaches", "leads", "stealers"}

func (s *BreachHouse) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	return darkEyeRun(ctx, darkEyeLookup{
		source:  s.Name(),
		product: breachHouseName,
		keys:    s.apiKeys,
		fetch: func(ctx context.Context, apiKey, domain string) ([]darkEyeRecord, error) {
			return s.fetch(ctx, session, apiKey, domain)
		},
	}, target, scanType)
}

// fetch runs one cross-source search.
func (s *BreachHouse) fetch(ctx context.Context, session *Session, apiKey, domain string) ([]darkEyeRecord, error) {
	q := url.Values{}
	q.Set("q", domain)
	q.Set("limit", strconv.Itoa(breachHouseLimit))
	endpoint := s.root() + "/search?" + q.Encode()

	body, err := darkEyeGet(ctx, session, breachHouseName, endpoint, apiKey, s.apiKeys)
	if err != nil {
		return nil, err
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil || data == nil {
		return nil, fmt.Errorf("%s returned an invalid response", breachHouseName)
	}

	var records []darkEyeRecord
	for _, category := range breachHouseCategories {
		entries := breachHouseEntries(data[category])
		if len(entries) >= breachHouseLimit {
			logger.Debugf("Breach House source reached the limit [%d] for %s", breachHouseLimit, category)
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
	return records, nil
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

// RateLimit is a conservative default (one request per second), not a documented limit.
func (s *BreachHouse) RateLimit() int {
	return 1
}
