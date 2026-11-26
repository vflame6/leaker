package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/vflame6/leaker/logger"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ProxyNovaResponse struct {
	Count int      `json:"count"`
	Lines []string `json:"lines"`
	// Error is set when ProxyNova refuses a query, e.g. when rate limited.
	Error string `json:"error"`
}

type ProxyNova struct {
}

// Search returns every result ProxyNova finds. ProxyNova has no search
// types, so the runner's result filter applies.
func (s *ProxyNova) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	p := newPager(q, s.Limits())
	page, err := s.fetchPage(ctx, q.Target, 0, p.size(), session)
	if err != nil {
		return p.stats(), err
	}
	for _, line := range page.Lines[:p.take(len(page.Lines))] {
		r := Result{Source: s.Name()}
		// ProxyNova returns lines in "email:password" format
		if idx := strings.Index(line, ":"); idx > 0 {
			r.Email = line[:idx]
			r.Password = line[idx+1:]
		} else {
			r.Email = line
		}
		if r.HasData() {
			emit(r)
		}
	}
	// ProxyNova serves the first 100 results only and refuses other pages
	// ("You are limited to 100 results").
	p.next(len(page.Lines), page.Count, false)
	return p.stats(), nil
}

// fetchPage retrieves limit results from ProxyNova starting at offset start.
func (s *ProxyNova) fetchPage(ctx context.Context, target string, start, limit int, session *Session) (*ProxyNovaResponse, error) {
	query := url.Values{}
	query.Set("query", target)
	query.Set("start", strconv.Itoa(start))
	query.Set("limit", strconv.Itoa(limit))
	endpoint := "https://api.proxynova.com/comb?" + query.Encode()
	logger.Debugf("Sending a request in ProxyNova source for %s (start=%d)", target, start)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := session.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	logger.Debugf("Response from ProxyNova source: status code [%d], size [%d]", resp.StatusCode, len(body))

	var response ProxyNovaResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse ProxyNova response: %s", errorBody(body))
	}
	if response.Error != "" {
		return nil, fmt.Errorf("ProxyNova refused the query: %s", errorBody([]byte(response.Error)))
	}

	return &response, nil
}

// Name returns the name of the source
// Limits: ProxyNova serves the first 100 results only.
func (s *ProxyNova) Limits() Limits { return Limits{PageSize: 100, MaxResults: 100} }

func (s *ProxyNova) Name() string {
	return "proxynova"
}

func (s *ProxyNova) UsesKey() bool {
	return false
}

func (s *ProxyNova) NeedsKey() bool {
	return false
}

func (s *ProxyNova) AddApiKeys(_ []string) {
	// no key needed
}

// Types: ProxyNova searches free text, so every type is searched as text.
func (s *ProxyNova) Types() []ScanType { return AllTypes }

// HasKeys reports whether a usable API key is configured.
func (s *ProxyNova) HasKeys() bool {
	return false
}

// MinInterval follows https://www.proxynova.com/tools/comb/:
// "You are limited to about 100 requests per minute." 667ms is about 90 per minute.
func (s *ProxyNova) MinInterval() time.Duration {
	return 667 * time.Millisecond
}
