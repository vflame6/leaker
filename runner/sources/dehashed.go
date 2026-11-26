package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type DeHashed struct {
	apiKeys []string
}

// dehashedMaxResults is the most entries one query returns (page*size).
const dehashedMaxResults = 10000

type dehashedSearchRequest struct {
	Query  string `json:"query"`
	Page   int    `json:"page"`
	Size   int    `json:"size"`
	DeDupe bool   `json:"de_dupe"`
}

type dehashedSearchResponse struct {
	// Balance is the number of credits left on the key.
	Balance *int            `json:"balance"`
	Entries []dehashedEntry `json:"entries"`
	// Total is the number of entries matching the query.
	Total *int `json:"total"`
}

// dehashedEntry is one record. API v2 sends every field as an array; a plain
// string is accepted too.
type dehashedEntry struct {
	Email          dehashedValues `json:"email"`
	IPAddress      dehashedValues `json:"ip_address"`
	Username       dehashedValues `json:"username"`
	Password       dehashedValues `json:"password"`
	HashedPassword dehashedValues `json:"hashed_password"`
	Name           dehashedValues `json:"name"`
	Phone          dehashedValues `json:"phone"`
	URL            dehashedValues `json:"url"`
	DOB            dehashedValues `json:"dob"`
	Address        dehashedValues `json:"address"`
	Company        dehashedValues `json:"company"`
	DatabaseName   string         `json:"database_name"`
}

// dehashedValues is a field given as a string or an array of strings.
type dehashedValues []string

func (v *dehashedValues) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*v = list
		return nil
	}
	var one *string
	if err := json.Unmarshal(data, &one); err != nil {
		return err
	}
	*v = nil
	if one != nil {
		*v = dehashedValues{*one}
	}
	return nil
}

// set puts the first value into *field, the value containing target first
// so the result filter sees it, and the others into Extra as name_2, ...
func (v dehashedValues) set(r *Result, field *string, name, target string) {
	values := make([]string, 0, len(v))
	for _, value := range v {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return
	}
	lowerTarget := strings.ToLower(target)
	for i, value := range values {
		if i > 0 && lowerTarget != "" && strings.Contains(strings.ToLower(value), lowerTarget) {
			values[0], values[i] = values[i], values[0]
			break
		}
	}
	start := 0
	if field != nil {
		*field = values[0]
		start = 1
	}
	for i := start; i < len(values); i++ {
		key := name
		if i > 0 {
			key = fmt.Sprintf("%s_%d", name, i+1)
		}
		r.SetExtra(key, values[i])
	}
}

func (s *DeHashed) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	target := q.Target
	randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if randomApiKey == "" {
		return newStats(), nil
	}

	// Build query with field prefix based on scan type
	var query string
	switch q.Type {
	case TypeEmail:
		query = "email:" + target
	case TypeUsername:
		query = "username:" + target
	case TypeDomain:
		query = "email:*@" + target
	case TypeKeyword:
		query = target
	case TypePhone:
		query = "phone:" + target
	}

	// Every request costs a credit: ask for as many entries per page as
	// wanted, up to the 10,000 a query can return.
	p := newPager(q, s.Limits())
	var balance *int
	for page := 1; ; page++ {
		response, err := s.fetchPage(ctx, session, randomApiKey, query, page, p.size())
		if err != nil {
			stats := p.stats()
			stats.CreditsLeft = balance
			return stats, err
		}
		balance = response.Balance
		total := -1
		if response.Total != nil {
			total = *response.Total
		}
		for _, entry := range response.Entries[:p.take(len(response.Entries))] {
			r := Result{Source: s.Name(), Database: entry.DatabaseName}
			entry.Email.set(&r, &r.Email, "email", target)
			entry.Username.set(&r, &r.Username, "username", target)
			entry.Password.set(&r, &r.Password, "password", target)
			entry.HashedPassword.set(&r, &r.Hash, "hash", target)
			entry.Name.set(&r, &r.Name, "name", target)
			entry.Phone.set(&r, &r.Phone, "phone", target)
			entry.IPAddress.set(&r, &r.IP, "ip", target)
			entry.URL.set(&r, &r.URL, "url", target)
			entry.DOB.set(&r, nil, "dob", target)
			entry.Address.set(&r, nil, "address", target)
			entry.Company.set(&r, nil, "company", target)
			if r.HasData() {
				emit(r)
			}
		}
		// page*size may not pass the query's 10,000 entries.
		more := (page+1)*p.size() <= dehashedMaxResults
		if !p.next(len(response.Entries), total, more) {
			break
		}
	}
	stats := p.stats()
	stats.CreditsLeft = balance
	return stats, nil
}

// fetchPage sends one search request.
func (s *DeHashed) fetchPage(ctx context.Context, session *Session, apiKey, query string, page, size int) (dehashedSearchResponse, error) {
	var response dehashedSearchResponse
	body, err := json.Marshal(dehashedSearchRequest{Query: query, Page: page, Size: size, DeDupe: true})
	if err != nil {
		return response, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.dehashed.com/v2/search", bytes.NewReader(body))
	if err != nil {
		return response, err
	}
	req.Header.Set("Dehashed-Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in DeHashed source (page %d, size %d)", page, size)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return response, dehashedError(err)
	}
	defer session.DiscardHTTPResponse(resp)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return response, err
	}
	logger.Debugf("Response from DeHashed source: status code [%d], size [%d]", resp.StatusCode, len(respBody))
	err = json.Unmarshal(respBody, &response)
	return response, err
}

// dehashedError gives DeHashed's documented answers their kind: 403 is
// "Insufficient Credits" and a 400 about authentication is a bad key.
func dehashedError(err error) error {
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		return err
	}
	switch {
	case statusErr.StatusCode == http.StatusForbidden:
		return withKind(err, ErrQuota)
	case statusErr.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(statusErr.Body), "authentication"):
		return withKind(err, ErrAuth)
	}
	return err
}

// Types: DeHashed searches every field, and free text for keywords.
func (s *DeHashed) Types() []ScanType { return AllTypes }

// Limits: up to 10,000 entries per request and per query.
func (s *DeHashed) Limits() Limits {
	return Limits{PageSize: dehashedMaxResults, MaxResults: dehashedMaxResults}
}

func (s *DeHashed) Name() string {
	return "dehashed"
}

func (s *DeHashed) UsesKey() bool {
	return true
}

func (s *DeHashed) NeedsKey() bool {
	return true
}

func (s *DeHashed) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// HasKeys reports whether a usable API key is configured.
func (s *DeHashed) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval returns the minimum time between two requests to the source.
func (s *DeHashed) MinInterval() time.Duration {
	return 100 * time.Millisecond
}
