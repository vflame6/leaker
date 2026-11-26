package sources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type WhiteIntel struct {
	apiKeys []string
}

type whiteIntelRequest struct {
	APIKey   string `json:"apikey"`
	Query    string `json:"query"`
	Type     string `json:"type"`
	Limit    int    `json:"limit"`
	Page     int    `json:"page"`
	Username string `json:"username,omitempty"`
}

type whiteIntelResult struct {
	DataType    string `json:"data_type"`
	URL         string `json:"url"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	LogDate     string `json:"log_date"`
	Hostname    string `json:"hostname"`
	IP          string `json:"ip"`
	MalwarePath string `json:"malware_path"`
}

type whiteIntelResponse struct {
	Success bool `json:"success"`
	// RemainingDailyCalls is the number of calls the key has left today.
	RemainingDailyCalls *int               `json:"remaining_daily_calls"`
	Results             []whiteIntelResult `json:"results"`
}

func (s *WhiteIntel) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if randomApiKey == "" {
		return newStats(), nil
	}
	if !SupportsScanType(s, q.Type) {
		return newStats(), unsupportedType(s, q.Type)
	}

	p := newPager(q, s.Limits())
	var remaining *int
	for page := 1; ; page++ {
		records, left, fingerprint, err := s.fetchPage(ctx, session, randomApiKey, q, page, p.size())
		if left != nil {
			remaining = left
		}
		if err != nil {
			stats := p.stats()
			stats.CreditsLeft = remaining
			return stats, err
		}
		// An API that ignores the page number sends the same page again.
		if p.repeated(fingerprint) {
			break
		}
		for _, r := range records[:p.take(len(records))] {
			if r.HasData() {
				emit(r)
			}
		}
		// The API gives no total: a short page is the last one.
		if !p.next(len(records), -1, len(records) == p.size()) {
			break
		}
	}
	stats := p.stats()
	stats.CreditsLeft = remaining
	return stats, nil
}

// fetchPage reads one page of limit records, the calls left today and a
// fingerprint of the page.
func (s *WhiteIntel) fetchPage(ctx context.Context, session *Session, apiKey string, q Query, page, limit int) ([]Result, *int, string, error) {
	searchReq := whiteIntelRequest{
		APIKey: apiKey,
		Query:  q.Target,
		Type:   "all",
		Limit:  limit,
		Page:   page,
	}
	if q.Type == TypeUsername {
		searchReq.Username = q.Target
	}

	body, err := json.Marshal(searchReq)
	if err != nil {
		return nil, nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.whiteintel.io/get_consumer_leaks.php",
		bytes.NewReader(body))
	if err != nil {
		return nil, nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in WhiteIntel source for %s (page %d)", q.Target, page)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return nil, nil, "", whiteIntelError(err)
	}
	defer session.DiscardHTTPResponse(resp)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, "", err
	}
	logger.Debugf("Response from WhiteIntel source: status code [%d], size [%d]", resp.StatusCode, len(respBody))

	var response whiteIntelResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, nil, "", err
	}
	if !response.Success {
		return nil, response.RemainingDailyCalls, "", fmt.Errorf("WhiteIntel request failed: %s", errorBody(respBody, s.apiKeys...))
	}

	records := make([]Result, 0, len(response.Results))
	for _, record := range response.Results {
		r := Result{
			Source:   s.Name(),
			Username: record.Username,
			Password: record.Password,
			IP:       record.IP,
			URL:      record.URL,
		}
		if record.DataType != "" {
			r.SetExtra("data_type", record.DataType)
		}
		if record.Hostname != "" {
			r.SetExtra("hostname", record.Hostname)
		}
		if record.LogDate != "" {
			r.SetExtra("log_date", record.LogDate)
		}
		if record.MalwarePath != "" {
			r.SetExtra("malware_path", record.MalwarePath)
		}
		records = append(records, r)
	}
	sum := sha256.Sum256(respBody)
	return records, response.RemainingDailyCalls, hex.EncodeToString(sum[:]), nil
}

// whiteIntelError: WhiteIntel answers 403 "Daily limit is reached." when the
// key has no calls left today; other 403s are a bad key or tier.
func whiteIntelError(err error) error {
	var statusErr *StatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusForbidden &&
		strings.Contains(strings.ToLower(statusErr.Body), "daily limit") {
		return withKind(err, ErrQuota)
	}
	return err
}

// Limits: up to 5,000 results per page (documented maximum).
func (s *WhiteIntel) Limits() Limits { return Limits{PageSize: 5000} }

// Types: WhiteIntel has no keyword or phone search.
func (s *WhiteIntel) Types() []ScanType { return []ScanType{TypeEmail, TypeUsername, TypeDomain} }

func (s *WhiteIntel) Name() string {
	return "whiteintel"
}

func (s *WhiteIntel) UsesKey() bool {
	return true
}

func (s *WhiteIntel) NeedsKey() bool {
	return true
}

func (s *WhiteIntel) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// HasKeys reports whether a usable API key is configured.
func (s *WhiteIntel) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval follows the API's 429 answer: "Please wait 5 seconds between
// requests."
func (s *WhiteIntel) MinInterval() time.Duration {
	return 5 * time.Second
}
