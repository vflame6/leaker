package sources

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type BreachDirectory struct {
	apiKeys []string
}

type breachDirectoryResponse struct {
	Found  int                    `json:"found"`
	Result []breachDirectoryEntry `json:"result"`
}

type breachDirectoryEntry struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Sha1     string `json:"sha1"`
	Hash     string `json:"hash"`
	Sources  string `json:"sources"`
}

func (s *BreachDirectory) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	target := q.Target
	stats := newStats()

	randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if randomApiKey == "" {
		return stats, nil
	}

	// BreachDirectory supports auto-detection of input type
	query := url.Values{}
	query.Set("func", "auto")
	query.Set("term", target)
	endpoint := "https://breachdirectory.p.rapidapi.com/?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return stats, err
	}
	req.Header.Set("x-rapidapi-key", randomApiKey)
	req.Header.Set("x-rapidapi-host", "breachdirectory.p.rapidapi.com")
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in BreachDirectory source for %s", target)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return stats, err
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return stats, err
	}
	logger.Debugf("Response from BreachDirectory source: status code [%d], size [%d]", resp.StatusCode, len(body))

	var response breachDirectoryResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return stats, err
	}
	stats.Total = response.Found

	for _, entry := range response.Result {
		r := Result{
			Source:   s.Name(),
			Email:    entry.Email,
			Password: entry.Password,
		}
		// Prefer hash over sha1; store sha1 in hash field if hash is empty
		if entry.Hash != "" {
			r.Hash = entry.Hash
		} else if entry.Sha1 != "" {
			r.Hash = entry.Sha1
		}
		if entry.Sources != "" && entry.Sources != "Unknown" {
			r.Database = entry.Sources
		}
		if r.HasData() {
			emit(r)
		}
	}
	return stats, nil
}

// Types: func=auto lets the API detect what the target is.
func (s *BreachDirectory) Types() []ScanType { return AllTypes }

// Limits: one answer holds every record.
func (s *BreachDirectory) Limits() Limits { return Limits{} }

func (s *BreachDirectory) Name() string {
	return "breachdirectory"
}

func (s *BreachDirectory) UsesKey() bool {
	return true
}

func (s *BreachDirectory) NeedsKey() bool {
	return true
}

func (s *BreachDirectory) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// HasKeys reports whether a usable API key is configured.
func (s *BreachDirectory) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval: the free tier allows 10 requests per month; paid tiers allow more.
func (s *BreachDirectory) MinInterval() time.Duration {
	return time.Second
}
