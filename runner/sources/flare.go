package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

// Flare queries the credentials of Flare's Account & Session Takeover
// Prevention (ASTP) for a domain or an email address. It needs the ASTP
// licence. Built from the vendor documentation (api.docs.flare.io); not yet
// checked against a live key.
type Flare struct {
	apiKeys []string

	// baseURL overrides the API root (tests only).
	baseURL string

	mu     sync.Mutex
	tokens map[string]flareToken // by API key
}

// flareToken is a session token made from an API key.
type flareToken struct {
	value   string
	expires time.Time
}

const (
	flareBaseURL = "https://api.flare.io"
	// flareTokenLife is how long a token is used; Flare's expire after an
	// hour.
	flareTokenLife = 50 * time.Minute
)

type flareSearchRequest struct {
	Query flareQuery `json:"query"`
	Size  int        `json:"size,omitempty"`
	From  string     `json:"from,omitempty"`
	Order string     `json:"order"`
}

type flareQuery struct {
	Type  string `json:"type"`
	FQDN  string `json:"fqdn,omitempty"`
	Email string `json:"email,omitempty"`
}

type flarePage struct {
	Items []flareItem `json:"items"`
	// Next is the cursor of the following page; null on the last one.
	Next *string `json:"next"`
}

type flareItem struct {
	IdentityName string   `json:"identity_name"`
	Hash         string   `json:"hash"`
	HashType     *string  `json:"hash_type"`
	ImportedAt   string   `json:"imported_at"`
	SourceID     string   `json:"source_id"`
	AuthDomains  []string `json:"auth_domains"`
	Source       struct {
		Name string `json:"name"`
	} `json:"source"`
}

func (s *Flare) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	var query flareQuery
	switch q.Type {
	case TypeDomain:
		query = flareQuery{Type: "domain", FQDN: q.Target}
	case TypeEmail:
		query = flareQuery{Type: "email", Email: q.Target}
	default:
		return newStats(), unsupportedType(s, q.Type)
	}
	s.mu.Lock()
	apiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	s.mu.Unlock()
	if apiKey == "" {
		return newStats(), nil
	}
	token, err := s.token(ctx, session, apiKey)
	if err != nil {
		return newStats(), err
	}

	// Cursor paging: only a missing next ends the search, not an empty page.
	p := newPager(q, s.Limits())
	from := ""
	for {
		page, err := s.fetchPage(ctx, session, token, flareSearchRequest{Query: query, Size: p.size(), From: from, Order: "desc"})
		if err != nil {
			return p.stats(), err
		}
		for _, item := range page.Items[:p.take(len(page.Items))] {
			if r := s.itemToResult(item); r.HasData() {
				emit(r)
			}
		}
		from = ""
		if page.Next != nil {
			from = *page.Next
		}
		if from != "" && p.repeated(from) {
			return p.stats(), nil
		}
		if !p.next(len(page.Items), -1, from != "") {
			return p.stats(), nil
		}
	}
}

// token returns a session token for apiKey, made once an hour.
func (s *Flare) token(ctx context.Context, session *Session, apiKey string) (string, error) {
	s.mu.Lock()
	cached, ok := s.tokens[apiKey]
	s.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.value, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.root()+"/tokens/generate", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a token request in Flare source")
	resp, err := session.Do(ctx, req)
	if err != nil {
		return "", err
	}
	defer session.DiscardHTTPResponse(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var answer struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Token == "" {
		return "", fmt.Errorf("Flare returned no token")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = make(map[string]flareToken)
	}
	s.tokens[apiKey] = flareToken{value: answer.Token, expires: time.Now().Add(flareTokenLife)}
	return answer.Token, nil
}

// fetchPage sends one search request.
func (s *Flare) fetchPage(ctx context.Context, session *Session, token string, search flareSearchRequest) (flarePage, error) {
	var page flarePage
	body, err := json.Marshal(search)
	if err != nil {
		return page, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.root()+"/astp/v2/credentials/_search", bytes.NewReader(body))
	if err != nil {
		return page, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in Flare source")
	resp, err := session.Do(ctx, req)
	if err != nil {
		return page, err
	}
	defer session.DiscardHTTPResponse(resp)
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return page, err
	}
	logger.Debugf("Response from Flare source: status code [%d], size [%d]", resp.StatusCode, len(respBody))
	err = json.Unmarshal(respBody, &page)
	return page, err
}

// itemToResult maps one credential. "hash" holds the password itself when
// hash_type is null.
func (s *Flare) itemToResult(item flareItem) Result {
	r := Result{Source: s.Name(), Database: strings.TrimSpace(item.Source.Name)}
	if r.Database == "" {
		r.Database = strings.TrimSpace(item.SourceID)
	}
	identity := strings.TrimSpace(item.IdentityName)
	if strings.Contains(identity, "@") {
		r.Email = identity
	} else {
		r.Username = identity
	}
	if item.HashType == nil || strings.TrimSpace(*item.HashType) == "" {
		r.Password = item.Hash
	} else {
		r.Hash = item.Hash
		r.SetExtra("hash_type", strings.TrimSpace(*item.HashType))
	}
	if day := darkEyeDate(item.ImportedAt); day != "" {
		r.SetExtra("imported_at", day)
	}
	var domains []string
	for _, d := range item.AuthDomains {
		if d = strings.TrimSpace(d); d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) > 0 {
		r.SetExtra("auth_domains", strings.Join(domains, ", "))
	}
	return r
}

func (s *Flare) root() string {
	if s.baseURL != "" {
		return strings.TrimRight(s.baseURL, "/")
	}
	return flareBaseURL
}

// Types: Flare's credential queries take a domain or an email address.
func (s *Flare) Types() []ScanType { return []ScanType{TypeEmail, TypeDomain} }

// Limits: up to 10,000 credentials per page.
func (s *Flare) Limits() Limits { return Limits{PageSize: 10000} }

func (s *Flare) Name() string { return "flare" }

func (s *Flare) UsesKey() bool { return true }

func (s *Flare) NeedsKey() bool { return true }

// AddApiKeys sets the keys and forgets the tokens of the previous ones.
func (s *Flare) AddApiKeys(keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiKeys = keys
	s.tokens = nil
}

// HasKeys reports whether a usable API key is configured.
func (s *Flare) HasKeys() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.apiKeys) > 0
}

// MinInterval follows the export guide: one second between requests.
func (s *Flare) MinInterval() time.Duration { return time.Second }
