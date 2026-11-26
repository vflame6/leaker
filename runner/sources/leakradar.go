package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

const leakRadarDefaultBaseURL = "https://api.leakradar.io"

// Page sizes: the API accepts up to 100 leaks per email page and 1,000 per
// domain page. --max-requests bounds the pages, and with them the points
// the auto-unlock spends.
const (
	leakRadarEmailPageSize  = 100
	leakRadarDomainPageSize = 1000
)

type LeakRadar struct {
	apiKeys []string
	baseURL string
}

type leakRadarEmailSearchRequest struct {
	Email   string `json:"email"`
	Search  string `json:"search,omitempty"`
	IsEmail *bool  `json:"is_email,omitempty"`
}

type leakRadarSearchResponse struct {
	Items                    []leakRadarLeak `json:"items"`
	Total                    int             `json:"total"`
	TotalUnlocked            int             `json:"total_unlocked"`
	Page                     int             `json:"page"`
	PageSize                 int             `json:"page_size"`
	AutoUnlockPointsConsumed *int            `json:"auto_unlock_points_consumed,omitempty"`
}

type leakRadarLeak struct {
	ID               string `json:"id"`
	URL              string `json:"url"`
	Username         string `json:"username"`
	UsernameMasked   string `json:"username_masked"`
	Password         string `json:"password"`
	PasswordStrength *int   `json:"password_strength"`
	Unlocked         bool   `json:"unlocked"`
	IsEmail          *bool  `json:"is_email"`
	AddedAt          string `json:"added_at"`
	Status           string `json:"status"`
	Category         string `json:"category"`
}

func (s *LeakRadar) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	apiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if apiKey == "" {
		return newStats(), nil
	}

	var (
		pages leakRadarPages
		err   error
	)
	switch q.Type {
	case TypeEmail:
		isEmail := true
		pages, err = s.searchEmail(ctx, session, apiKey, q, &isEmail)
	case TypeUsername:
		isEmail := false
		pages, err = s.searchEmail(ctx, session, apiKey, q, &isEmail)
	case TypeDomain:
		pages, err = s.searchDomain(ctx, session, apiKey, q)
	default:
		return newStats(), unsupportedType(s, q.Type)
	}
	for _, leak := range pages.leaks {
		if result, ok := s.leakToResult(leak); ok {
			emit(result)
		}
	}
	return pages.stats, err
}

// leakRadarPages is what the pages of one search returned.
type leakRadarPages struct {
	leaks []leakRadarLeak
	stats Stats
}

func (s *LeakRadar) searchEmail(ctx context.Context, session *Session, apiKey string, q Query, isEmail *bool) (leakRadarPages, error) {
	body, err := json.Marshal(leakRadarEmailSearchRequest{
		Email:   q.Target,
		IsEmail: isEmail,
	})
	if err != nil {
		return leakRadarPages{stats: newStats()}, err
	}

	p := newPager(q, Limits{PageSize: leakRadarEmailPageSize})
	return s.searchPages(ctx, session, p, func(page, size int) (*http.Request, error) {
		endpoint, err := url.Parse(s.apiBaseURL() + "/search/email")
		if err != nil {
			return nil, err
		}
		values := endpoint.Query()
		values.Set("page", strconv.Itoa(page))
		values.Set("page_size", strconv.Itoa(size))
		values.Set("auto_unlock", "true")
		endpoint.RawQuery = values.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in LeakRadar source for %s (page %d)", q.Target, page)
		return req, nil
	})
}

func (s *LeakRadar) searchDomain(ctx context.Context, session *Session, apiKey string, q Query) (leakRadarPages, error) {
	p := newPager(q, Limits{PageSize: leakRadarDomainPageSize})
	return s.searchPages(ctx, session, p, func(page, size int) (*http.Request, error) {
		endpoint, err := url.Parse(s.apiBaseURL() + "/search/domain/" + url.PathEscape(q.Target) + "/all")
		if err != nil {
			return nil, err
		}
		values := endpoint.Query()
		values.Set("page", strconv.Itoa(page))
		values.Set("page_size", strconv.Itoa(size))
		values.Set("auto_unlock", "true")
		endpoint.RawQuery = values.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in LeakRadar source for %s (page %d)", q.Target, page)
		return req, nil
	})
}

// searchPages reads pages until the pager stops. It adds up the points the
// auto-unlock spent and keeps the searches left today. Leaks read before a
// failure are returned together with the error.
func (s *LeakRadar) searchPages(ctx context.Context, session *Session, p *pager,
	newRequest func(page, size int) (*http.Request, error)) (leakRadarPages, error) {
	var (
		leaks        []leakRadarLeak
		points       *int
		searchesLeft *int
	)
	done := func(err error) (leakRadarPages, error) {
		stats := p.stats()
		stats.CreditsUsed, stats.CreditsLeft = points, searchesLeft
		return leakRadarPages{leaks: leaks, stats: stats}, err
	}

	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return done(err)
		}
		req, err := newRequest(page, p.size())
		if err != nil {
			return done(err)
		}
		response, header, err := s.doSearch(session, req)
		if err != nil {
			return done(err)
		}
		leaks = append(leaks, response.Items[:p.take(len(response.Items))]...)
		if response.AutoUnlockPointsConsumed != nil {
			used := *response.AutoUnlockPointsConsumed
			if points != nil {
				used += *points
			}
			points = &used
		}
		if left, err := strconv.Atoi(header.Get("X-RateLimit-Daily-Remaining")); err == nil {
			searchesLeft = &left
		}

		pageSize := response.PageSize
		if pageSize <= 0 {
			pageSize = p.size()
		}
		currentPage := response.Page
		if currentPage <= 0 {
			currentPage = page
		}
		if !p.next(len(response.Items), response.Total, currentPage*pageSize < response.Total) {
			return done(nil)
		}
	}
}

// doSearch sends one request and returns the answer and its headers.
func (s *LeakRadar) doSearch(session *Session, req *http.Request) (leakRadarSearchResponse, http.Header, error) {
	resp, err := session.Do(req.Context(), req)
	if err != nil {
		return leakRadarSearchResponse{}, nil, err
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return leakRadarSearchResponse{}, nil, err
	}
	logger.Debugf("Response from LeakRadar source: status code [%d], size [%d]", resp.StatusCode, len(body))

	var response leakRadarSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return leakRadarSearchResponse{}, nil, err
	}
	return response, resp.Header, nil
}

func (s *LeakRadar) leakToResult(leak leakRadarLeak) (Result, bool) {
	if !leak.Unlocked {
		return Result{}, false
	}

	r := Result{
		Source:   s.Name(),
		Password: leak.Password,
		URL:      leak.URL,
	}
	if leak.Username != "" {
		switch {
		case leak.IsEmail != nil:
			if *leak.IsEmail {
				r.Email = leak.Username
			} else {
				r.Username = leak.Username
			}
		case strings.Contains(leak.Username, "@"):
			r.Email = leak.Username
		default:
			r.Username = leak.Username
		}
	}

	if r.Email == "" && r.Username == "" && r.Password == "" && r.URL == "" {
		return Result{}, false
	}

	if leak.ID != "" {
		r.SetExtra("leak_id", leak.ID)
	}
	if leak.PasswordStrength != nil {
		r.SetExtra("password_strength", strconv.Itoa(*leak.PasswordStrength))
	}
	if leak.Status != "" {
		r.SetExtra("status", leak.Status)
	}
	if leak.AddedAt != "" {
		r.SetExtra("added_at", leak.AddedAt)
	}
	if leak.Category != "" {
		r.SetExtra("category", leak.Category)
	}

	return r, true
}

func (s *LeakRadar) apiBaseURL() string {
	if s.baseURL != "" {
		return strings.TrimRight(s.baseURL, "/")
	}
	return leakRadarDefaultBaseURL
}

// Limits: email and username pages hold up to 100 leaks, domain pages up
// to 1,000.
func (s *LeakRadar) Limits() Limits { return Limits{PageSize: leakRadarEmailPageSize} }

func (s *LeakRadar) Name() string {
	return "leakradar"
}

func (s *LeakRadar) UsesKey() bool {
	return true
}

func (s *LeakRadar) NeedsKey() bool {
	return true
}

func (s *LeakRadar) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// Types: LeakRadar has no keyword or phone search.
func (s *LeakRadar) Types() []ScanType { return []ScanType{TypeEmail, TypeUsername, TypeDomain} }

// HasKeys reports whether a usable API key is configured.
func (s *LeakRadar) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval returns the minimum time between two requests to the source.
func (s *LeakRadar) MinInterval() time.Duration {
	return 200 * time.Millisecond
}
