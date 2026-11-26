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

// HIBR queries Have I Been Ransomed? (haveibeenransom.com), one of the two
// DarkEye APIs: ransomware leak-site posts and data breaches for a domain.
// Shared DarkEye code is in darkeye.go.
type HIBR struct {
	apiKeys []string

	// baseURL overrides the API root (tests only).
	baseURL string
}

// Every page counts as one request against the key's quota; --max-requests
// caps the pages of one lookup.
const (
	hibrBaseURL = "https://haveibeenransom.com"
	hibrName    = "Have I Been Ransomed?"
)

// hibrCategories maps the HIBR leak_type values to the category names.
var hibrCategories = map[string]string{
	"ransomware_leak":    "ransomware",
	"traditional_breach": "breaches",
}

type hibrPage struct {
	Sources    []json.RawMessage `json:"sources"`
	Results    []json.RawMessage `json:"results"`
	HasNext    *bool             `json:"has_next"`
	Pagination struct {
		HasNext bool `json:"has_next"`
	} `json:"pagination"`
}

func (s *HIBR) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	return darkEyeSearch(ctx, darkEyeLookup{
		source:        s.Name(),
		product:       hibrName,
		keys:          s.apiKeys,
		keyedByDomain: true,
		fetch: func(ctx context.Context, q Query, apiKey, domain string) ([]darkEyeRecord, Stats, error) {
			return s.fetch(ctx, session, q, apiKey, domain)
		},
	}, q, emit)
}

// fetch reads pages of records while the API has a next page, within the
// limits of q. Records read before a failure are returned with the error.
func (s *HIBR) fetch(ctx context.Context, session *Session, q Query, apiKey, domain string) ([]darkEyeRecord, Stats, error) {
	p := newPager(q, s.Limits())
	var records []darkEyeRecord
	seen := make(map[string]struct{})

	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return records, p.stats(), err
		}

		endpoint := s.root() + "/api/metadata/domain/" + url.PathEscape(domain)
		if page > 1 {
			endpoint += "/" + strconv.Itoa(page)
		}

		body, err := darkEyeGet(ctx, session, hibrName, endpoint, apiKey, s.apiKeys)
		if err != nil {
			return records, p.stats(), err
		}

		var data hibrPage
		if err := json.Unmarshal(body, &data); err != nil {
			return records, p.stats(), fmt.Errorf("%s returned an invalid response", hibrName)
		}

		// The API returns "sources"; its documentation shows "results".
		raw := data.Sources
		if len(raw) == 0 {
			raw = data.Results
		}
		var pageRecords []darkEyeRecord
		for _, entry := range raw {
			var item darkEyeItem
			if err := json.Unmarshal(entry, &item); err != nil {
				continue
			}
			// A leak indexed while paging is repeated on a later page.
			id := strings.TrimSpace(string(item.IDSource))
			if id == "" {
				id = strings.TrimSpace(string(item.ID))
			}
			if id != "" {
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
			}
			leakType := strings.TrimSpace(string(item.LeakType))
			category, known := hibrCategories[leakType]
			if !known {
				category = darkEyeClean(leakType)
			}
			pageRecords = append(pageRecords, darkEyeRecord{category: category, id: id, item: item})
		}
		records = append(records, pageRecords[:p.take(len(pageRecords))]...)

		// A page can be short or even empty and still have a next page.
		hasNext := data.Pagination.HasNext
		if data.HasNext != nil {
			hasNext = *data.HasNext
		}
		if !p.next(len(pageRecords), -1, hasNext) {
			return records, p.stats(), nil
		}
	}
}

func (s *HIBR) root() string {
	if s.baseURL != "" {
		return strings.TrimRight(s.baseURL, "/")
	}
	return hibrBaseURL
}

// Types: HIBR searches by domain only.
func (s *HIBR) Types() []ScanType { return []ScanType{TypeDomain} }

// Limits: the API sets the page size and gives no total.
func (s *HIBR) Limits() Limits { return Limits{} }

func (s *HIBR) Name() string {
	return "hibr"
}

func (s *HIBR) UsesKey() bool {
	return true
}

func (s *HIBR) NeedsKey() bool {
	return true
}

func (s *HIBR) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// HasKeys reports whether a usable API key is configured.
func (s *HIBR) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval is a conservative default (one request per second), not a documented limit.
func (s *HIBR) MinInterval() time.Duration {
	return time.Second
}
