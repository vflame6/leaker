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

// HIBR queries Have I Been Ransomed? (haveibeenransom.com), one of the two
// DarkEye APIs: ransomware leak-site posts and data breaches for a domain.
// Shared DarkEye code is in darkeye.go.
type HIBR struct {
	apiKeys []string

	// baseURL overrides the API root (tests only).
	baseURL string
	// noDelay disables the pause between pages (tests only).
	noDelay bool
	// maxPages overrides hibrMaxPages when set (tests only).
	maxPages int
}

const (
	hibrBaseURL = "https://haveibeenransom.com"
	hibrName    = "Have I Been Ransomed?"

	// Every page counts as one request against the key's quota, so the number
	// of pages fetched per lookup is capped.
	hibrMaxPages = 5
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

func (s *HIBR) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	return darkEyeRun(ctx, darkEyeLookup{
		source:        s.Name(),
		product:       hibrName,
		keys:          s.apiKeys,
		keyedByDomain: true,
		fetch: func(ctx context.Context, apiKey, domain string) ([]darkEyeRecord, error) {
			return s.fetch(ctx, session, apiKey, domain)
		},
	}, target, scanType)
}

// fetch reads up to hibrMaxPages pages of records.
// Records read before a failure are returned together with the error.
func (s *HIBR) fetch(ctx context.Context, session *Session, apiKey, domain string) ([]darkEyeRecord, error) {
	var records []darkEyeRecord
	seen := make(map[string]struct{})

	maxPages := hibrMaxPages
	if s.maxPages > 0 {
		maxPages = s.maxPages
	}

	for page := 1; page <= maxPages; page++ {
		if page > 1 && !darkEyePause(ctx, s.RateLimit(), s.noDelay) {
			return records, ctx.Err()
		}

		endpoint := s.root() + "/api/metadata/domain/" + url.PathEscape(domain)
		if page > 1 {
			endpoint += "/" + strconv.Itoa(page)
		}

		body, err := darkEyeGet(ctx, session, hibrName, endpoint, apiKey, s.apiKeys)
		if err != nil {
			return records, err
		}

		var data hibrPage
		if err := json.Unmarshal(body, &data); err != nil {
			return records, fmt.Errorf("%s returned an invalid response", hibrName)
		}

		// The API returns "sources"; its documentation shows "results".
		raw := data.Sources
		if len(raw) == 0 {
			raw = data.Results
		}
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
			records = append(records, darkEyeRecord{category: category, id: id, item: item})
		}

		// A page can be short or even empty and still have a next page.
		hasNext := data.Pagination.HasNext
		if data.HasNext != nil {
			hasNext = *data.HasNext
		}
		if !hasNext {
			return records, nil
		}
		if page == maxPages {
			logger.Debugf("HIBR source reached the page cap [%d] with more results available", maxPages)
		}
	}

	return records, nil
}

func (s *HIBR) root() string {
	if s.baseURL != "" {
		return strings.TrimRight(s.baseURL, "/")
	}
	return hibrBaseURL
}

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

// RateLimit is a conservative default (one request per second), not a documented limit.
func (s *HIBR) RateLimit() int {
	return 1
}
