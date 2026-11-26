package sources

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

// SpyCloud queries the SpyCloud Investigations API (licensed): breach,
// combolist and infostealer records by email, username, domain or phone
// number. Built from the vendor documentation (docs.spycloud.com); not yet
// checked against a live key.
type SpyCloud struct {
	apiKeys []string

	// baseURL overrides the API root (tests only).
	baseURL string
}

const (
	spyCloudBaseURL = "https://api.spycloud.io/investigations-v2"
	// spyCloudMasked is how the API shows a field outside the key's contract.
	spyCloudMasked = "********"
)

// spyCloudPaths is the record endpoint of each type.
var spyCloudPaths = map[ScanType]string{
	TypeEmail:    "emails",
	TypeUsername: "usernames",
	TypeDomain:   "domains",
	TypePhone:    "phone-numbers",
}

type spyCloudResponse struct {
	// Cursor is empty on the last page.
	Cursor string `json:"cursor"`
	// Hits is the number of matching records.
	Hits    *int             `json:"hits"`
	Results []spyCloudRecord `json:"results"`
}

type spyCloudRecord struct {
	SourceID          any      `json:"source_id"`
	Severity          any      `json:"severity"`
	PublishDate       string   `json:"spycloud_publish_date"`
	BreachTitle       string   `json:"breach_title"`
	BreachCategory    string   `json:"breach_category"`
	Email             string   `json:"email"`
	Username          string   `json:"username"`
	Password          string   `json:"password"`
	PasswordType      string   `json:"password_type"`
	PasswordPlaintext string   `json:"password_plaintext"`
	Salt              string   `json:"salt"`
	IPAddresses       []string `json:"ip_addresses"`
	Phone             string   `json:"phone"`
	FullName          string   `json:"full_name"`
	TargetURL         string   `json:"target_url"`
	UserHostname      string   `json:"user_hostname"`
	UserOS            string   `json:"user_os"`
}

func (s *SpyCloud) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	path, ok := spyCloudPaths[q.Type]
	if !ok {
		return newStats(), unsupportedType(s, q.Type)
	}
	apiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if apiKey == "" {
		return newStats(), nil
	}
	// Some servers read "+" in a path as a space; jane+tag@ must stay intact.
	endpoint := s.root() + "/breach/data/" + path + "/" + strings.ReplaceAll(url.PathEscape(q.Target), "+", "%2B")

	// Pages hold 1,000 records; the cursor says whether another follows.
	p := newPager(q, s.Limits())
	hits := -1
	done := func(err error) (Stats, error) {
		stats := p.stats()
		stats.Total = hits
		return stats, err
	}
	cursor := ""
	for {
		page, err := s.fetchPage(ctx, session, apiKey, endpoint, cursor)
		if err != nil {
			return done(err)
		}
		if page.Hits != nil {
			hits = *page.Hits
		}
		for _, record := range page.Results[:p.take(len(page.Results))] {
			if r := s.recordToResult(record); r.HasData() {
				emit(r)
			}
		}
		cursor = page.Cursor
		if cursor != "" && p.repeated(cursor) {
			return done(nil)
		}
		if !p.next(len(page.Results), -1, cursor != "") {
			return done(nil)
		}
	}
}

// fetchPage reads the page at cursor ("" for the first).
func (s *SpyCloud) fetchPage(ctx context.Context, session *Session, apiKey, endpoint, cursor string) (spyCloudResponse, error) {
	var page spyCloudResponse
	if cursor != "" {
		endpoint += "?" + url.Values{"cursor": {cursor}}.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return page, err
	}
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in SpyCloud source")
	resp, err := session.Do(ctx, req)
	if err != nil {
		return page, spyCloudError(err)
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return page, err
	}
	logger.Debugf("Response from SpyCloud source: status code [%d], size [%d]", resp.StatusCode, len(body))
	err = json.Unmarshal(body, &page)
	return page, err
}

// recordToResult maps one record. The password of a record whose
// password_type is not plaintext is a hash; password_plaintext then holds
// what SpyCloud cracked.
func (s *SpyCloud) recordToResult(record spyCloudRecord) Result {
	r := Result{
		Source:   s.Name(),
		Email:    spyCloudValue(record.Email),
		Username: spyCloudValue(record.Username),
		Salt:     spyCloudValue(record.Salt),
		Phone:    spyCloudValue(record.Phone),
		Name:     spyCloudValue(record.FullName),
		URL:      spyCloudValue(record.TargetURL),
		Database: spyCloudValue(record.BreachTitle),
	}
	password := spyCloudValue(record.Password)
	switch passwordType := spyCloudValue(record.PasswordType); strings.ToLower(passwordType) {
	case "", "plaintext":
		r.Password = password
	default:
		r.Hash = password
		r.Password = spyCloudValue(record.PasswordPlaintext)
		r.SetExtra("password_type", passwordType)
	}
	var ips []string
	for _, ip := range record.IPAddresses {
		if ip = spyCloudValue(ip); ip != "" {
			ips = append(ips, ip)
		}
	}
	for i, ip := range ips {
		if i == 0 {
			r.IP = ip
		} else {
			r.SetExtra("ip_"+strconv.Itoa(i+1), ip)
		}
	}
	extra := map[string]string{
		"breach_category": spyCloudValue(record.BreachCategory),
		"severity":        jsonText(record.Severity),
		"published":       darkEyeDate(record.PublishDate),
		"source_id":       jsonText(record.SourceID),
		"user_hostname":   spyCloudValue(record.UserHostname),
		"user_os":         spyCloudValue(record.UserOS),
	}
	for key, value := range extra {
		if value != "" {
			r.SetExtra(key, value)
		}
	}
	return r
}

// spyCloudValue drops the masked value of a field outside the contract.
func spyCloudValue(value string) string {
	value = strings.TrimSpace(value)
	if value == spyCloudMasked {
		return ""
	}
	return value
}

// jsonText prints a JSON string or number.
func jsonText(value any) string {
	switch v := value.(type) {
	case string:
		return spyCloudValue(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// spyCloudError: SpyCloud answers 429 "Limit exceeded" once the monthly
// quota is spent, and 429 "Too many requests" above the rate limit.
func spyCloudError(err error) error {
	var statusErr *StatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusTooManyRequests &&
		strings.Contains(strings.ToLower(statusErr.Body), "limit exceeded") {
		return withKind(err, ErrQuota)
	}
	return err
}

func (s *SpyCloud) root() string {
	if s.baseURL != "" {
		return strings.TrimRight(s.baseURL, "/")
	}
	return spyCloudBaseURL
}

// Types: SpyCloud has no keyword search; IP searches have no leaker type.
func (s *SpyCloud) Types() []ScanType {
	return []ScanType{TypeEmail, TypeUsername, TypeDomain, TypePhone}
}

// Limits: pages of 1,000 records (fixed by the API).
func (s *SpyCloud) Limits() Limits { return Limits{PageSize: 1000} }

func (s *SpyCloud) Name() string { return "spycloud" }

func (s *SpyCloud) UsesKey() bool { return true }

func (s *SpyCloud) NeedsKey() bool { return true }

func (s *SpyCloud) AddApiKeys(keys []string) { s.apiKeys = keys }

// HasKeys reports whether a usable API key is configured.
func (s *SpyCloud) HasKeys() bool { return len(s.apiKeys) > 0 }

// MinInterval: domain searches are limited to one request per second per
// user; the other searches allow about ten.
func (s *SpyCloud) MinInterval() time.Duration { return time.Second }
