package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type WeLeakInfo struct {
	apiKeys []string
}

type weLeakInfoRequest struct {
	Query    string `json:"query"`
	Type     string `json:"type"`
	Limit    string `json:"limit"`
	Wildcard string `json:"wildcard"`
}

type weLeakInfoResponse struct {
	Data []map[string]interface{} `json:"Data"`
}

func (s *WeLeakInfo) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	target, scanType := q.Target, q.Type
	stats := newStats()

	randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if randomApiKey == "" {
		return stats, nil
	}

	// Extract private key from "pub_key:priv_key" format
	bearerToken := randomApiKey
	if idx := strings.LastIndex(randomApiKey, ":"); idx != -1 {
		bearerToken = randomApiKey[idx+1:]
	}

	p := newPager(q, s.Limits())
	searchReq := weLeakInfoRequest{
		Query:    target,
		Limit:    strconv.Itoa(p.size()),
		Wildcard: "false",
	}

	switch scanType {
	case TypeEmail:
		searchReq.Type = "email"
	case TypeUsername:
		searchReq.Type = "username"
	case TypeDomain:
		searchReq.Query = "*@" + target
		searchReq.Type = "email"
		searchReq.Wildcard = "true"
	default:
		return stats, unsupportedType(s, scanType)
	}

	body, err := json.Marshal(searchReq)
	if err != nil {
		return stats, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.weleakinfo.io/v3/search",
		bytes.NewReader(body))
	if err != nil {
		return stats, err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in WeLeakInfo source for %s", target)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return stats, err
	}
	defer session.DiscardHTTPResponse(resp)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return stats, err
	}
	logger.Debugf("Response from WeLeakInfo source: status code [%d], size [%d]", resp.StatusCode, len(respBody))

	var response weLeakInfoResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return stats, err
	}

	for _, record := range response.Data[:p.take(len(response.Data))] {
		r := Result{Source: s.Name()}
		if val, ok := record["email"].(string); ok && val != "" {
			r.Email = val
		}
		if val, ok := record["username"].(string); ok && val != "" {
			r.Username = val
		}
		if val, ok := record["password"].(string); ok && val != "" {
			r.Password = val
		}
		if val, ok := record["hash"].(string); ok && val != "" {
			r.Hash = val
		}
		if val, ok := record["name"].(string); ok && val != "" {
			r.Name = val
		}
		if val, ok := record["ip"].(string); ok && val != "" {
			r.IP = val
		}
		if val, ok := record["phone"].(string); ok && val != "" {
			r.Phone = val
		}
		if r.HasData() {
			emit(r)
		}
	}
	// One request: a full answer means more were left behind.
	p.next(len(response.Data), -1, len(response.Data) == p.size())
	return p.stats(), nil
}

// Types: WeLeakInfo has no keyword or phone search.
func (s *WeLeakInfo) Types() []ScanType { return []ScanType{TypeEmail, TypeUsername, TypeDomain} }

// Limits: one request of up to 1,000 records.
func (s *WeLeakInfo) Limits() Limits { return Limits{PageSize: 1000, MaxResults: 1000} }

func (s *WeLeakInfo) Name() string {
	return "weleakinfo"
}

func (s *WeLeakInfo) UsesKey() bool {
	return true
}

func (s *WeLeakInfo) NeedsKey() bool {
	return true
}

func (s *WeLeakInfo) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// HasKeys reports whether a usable API key is configured.
func (s *WeLeakInfo) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval returns the minimum time between two requests to the source.
func (s *WeLeakInfo) MinInterval() time.Duration {
	return 200 * time.Millisecond
}
