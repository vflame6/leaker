package sources

import (
	"bytes"
	"context"
	"encoding/json"
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

const leakRadarDefaultBaseURL = "https://api.leakradar.io"

const (
	leakRadarEmailPageSize  = 100
	leakRadarDomainPageSize = 1000
	// Bounds auto-unlock point spend and runtime while still covering large targets.
	leakRadarMaxPages = 10
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

func (s *LeakRadar) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		apiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
		if apiKey == "" {
			return
		}

		var (
			leaks []leakRadarLeak
			err   error
		)

		switch scanType {
		case TypeEmail:
			isEmail := true
			leaks, err = s.searchEmail(ctx, session, apiKey, target, &isEmail)
		case TypeUsername, TypeKeyword, TypePhone:
			isEmail := false
			leaks, err = s.searchEmail(ctx, session, apiKey, target, &isEmail)
		case TypeDomain:
			leaks, err = s.searchDomain(ctx, session, apiKey, target)
		default:
			return
		}
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		for _, leak := range leaks {
			result, ok := s.leakToResult(leak)
			if ok {
				results <- result
			}
		}
	}()

	return results
}

func (s *LeakRadar) searchEmail(ctx context.Context, session *Session, apiKey, target string, isEmail *bool) ([]leakRadarLeak, error) {
	body, err := json.Marshal(leakRadarEmailSearchRequest{
		Email:   target,
		IsEmail: isEmail,
	})
	if err != nil {
		return nil, err
	}

	return s.searchPages(ctx, session, leakRadarEmailPageSize, func(page int) (*http.Request, error) {
		endpoint, err := url.Parse(s.apiBaseURL() + "/search/email")
		if err != nil {
			return nil, err
		}
		q := endpoint.Query()
		q.Set("page", strconv.Itoa(page))
		q.Set("page_size", strconv.Itoa(leakRadarEmailPageSize))
		q.Set("auto_unlock", "true")
		endpoint.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in LeakRadar source for %s", target)
		return req, nil
	})
}

func (s *LeakRadar) searchDomain(ctx context.Context, session *Session, apiKey, target string) ([]leakRadarLeak, error) {
	return s.searchPages(ctx, session, leakRadarDomainPageSize, func(page int) (*http.Request, error) {
		endpoint, err := url.Parse(s.apiBaseURL() + "/search/domain/" + url.PathEscape(target) + "/all")
		if err != nil {
			return nil, err
		}
		q := endpoint.Query()
		q.Set("page", strconv.Itoa(page))
		q.Set("page_size", strconv.Itoa(leakRadarDomainPageSize))
		q.Set("auto_unlock", "true")
		endpoint.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in LeakRadar source for %s", target)
		return req, nil
	})
}

func (s *LeakRadar) searchPages(
	ctx context.Context,
	session *Session,
	defaultPageSize int,
	newRequest func(page int) (*http.Request, error),
) ([]leakRadarLeak, error) {
	var leaks []leakRadarLeak

	for page := 1; page <= leakRadarMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		req, err := newRequest(page)
		if err != nil {
			return nil, err
		}

		response, err := s.doSearch(session, req)
		if err != nil {
			return nil, err
		}
		leaks = append(leaks, response.Items...)

		pageSize := response.PageSize
		if pageSize <= 0 {
			pageSize = defaultPageSize
		}
		currentPage := response.Page
		if currentPage <= 0 {
			currentPage = page
		}
		if len(response.Items) == 0 || currentPage*pageSize >= response.Total {
			break
		}

		if page == leakRadarMaxPages {
			logger.Debugf("LeakRadar reached page cap [%d] with more results available", leakRadarMaxPages)
			break
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second / time.Duration(s.RateLimit())):
		}
	}

	return leaks, nil
}

func (s *LeakRadar) doSearch(session *Session, req *http.Request) (leakRadarSearchResponse, error) {
	resp, err := session.Client.Do(req)
	if err != nil {
		return leakRadarSearchResponse{}, err
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return leakRadarSearchResponse{}, err
	}
	logger.Debugf("Response from LeakRadar source: status code [%d], size [%d]", resp.StatusCode, len(body))

	if resp.StatusCode != http.StatusOK {
		return leakRadarSearchResponse{}, fmt.Errorf("LeakRadar returned status %d: %s", resp.StatusCode, string(body))
	}

	var response leakRadarSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return leakRadarSearchResponse{}, err
	}
	return response, nil
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

func (s *LeakRadar) RateLimit() int {
	return 5
}
