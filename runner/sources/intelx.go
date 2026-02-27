package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type IntelX struct {
	apiKeys []intelxKey
}

// intelxKey holds a parsed HOST:API_KEY pair.
type intelxKey struct {
	host   string // e.g. "2.intelx.io" or "free.intelx.io"
	apiKey string // UUID API key
}

// intelxSearchRequest is the request body for POST /intelligent/search
type intelxSearchRequest struct {
	Term       string `json:"term"`
	MaxResults int    `json:"maxresults"`
	Sort       int    `json:"sort"` // 4 = date desc
	Media      int    `json:"media"`
}

type intelxSearchResponse struct {
	ID     string `json:"id"`
	Status int    `json:"status"` // 0=success, 1=invalid term, 2=max concurrent
}

type intelxResultRecord struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MediaH      string `json:"mediah"`
	Date        string `json:"date"`
	Bucket      string `json:"bucket"`
}

type intelxResultResponse struct {
	Records []intelxResultRecord `json:"records"`
	Status  int                  `json:"status"` // 0=results(continue), 1=no more, 2=not found, 3=keep trying
}

func (s *IntelX) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		key := utils.PickRandom(s.apiKeys, s.Name())
		if key.apiKey == "" {
			return
		}
		randomApiKey := key.apiKey
		apiURL := fmt.Sprintf("https://%s/", key.host)

		// Start the search
		searchReq := intelxSearchRequest{
			Term:       target,
			MaxResults: 100,
			Sort:       4, // date desc
			Media:      0, // all media types
		}

		body, err := json.Marshal(searchReq)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"intelligent/search", bytes.NewReader(body))
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		req.Header.Set("x-key", randomApiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending search request in IntelX source for %s", target)
		resp, err := session.Client.Do(req)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		defer session.DiscardHTTPResponse(resp)

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		if resp.StatusCode != http.StatusOK {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("IntelX search returned status %d: %s", resp.StatusCode, string(respBody)),
			}
			return
		}

		var searchResp intelxSearchResponse
		if err := json.Unmarshal(respBody, &searchResp); err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		if searchResp.Status != 0 {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("IntelX search failed with status %d", searchResp.Status),
			}
			return
		}

		searchID := searchResp.ID
		logger.Debugf("IntelX search started with ID %s", searchID)

		// Poll for results (up to 10 attempts with 2s delay)
		for attempt := 0; attempt < 10; attempt++ {
			select {
			case <-ctx.Done():
				// Terminate search on context cancel
				s.terminateSearch(ctx, session, apiURL, randomApiKey, searchID)
				return
			case <-time.After(2 * time.Second):
			}

			resultReq, err := http.NewRequestWithContext(ctx, "GET",
				fmt.Sprintf("%sintelligent/search/result?id=%s&limit=100&previewlines=8", apiURL, searchID), nil)
			if err != nil {
				results <- Result{Source: s.Name(), Error: err}
				return
			}
			resultReq.Header.Set("x-key", randomApiKey)
			resultReq.Header.Set("Accept", "application/json")

			resultResp, err := session.Client.Do(resultReq)
			if err != nil {
				results <- Result{Source: s.Name(), Error: err}
				return
			}

			resultBody, err := io.ReadAll(resultResp.Body)
			session.DiscardHTTPResponse(resultResp)
			if err != nil {
				results <- Result{Source: s.Name(), Error: err}
				return
			}

			var resultData intelxResultResponse
			if err := json.Unmarshal(resultBody, &resultData); err != nil {
				results <- Result{Source: s.Name(), Error: err}
				return
			}

			// Emit records
			for _, record := range resultData.Records {
				value := s.formatRecord(record)
				if value != "" {
					results <- Result{Source: s.Name(), Value: value}
				}
			}

			// Status: 0=continue, 1=done, 2=not found, 3=keep trying
			if resultData.Status == 1 || resultData.Status == 2 {
				break
			}
			if resultData.Status == 0 {
				// More results may be available, continue polling
				continue
			}
			// Status 3: no results yet, keep trying
		}

		// Terminate search to free resources
		s.terminateSearch(ctx, session, apiURL, randomApiKey, searchID)
	}()

	return results
}

func (s *IntelX) terminateSearch(ctx context.Context, session *Session, apiURL, apiKey, searchID string) {
	terminateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(terminateCtx, "GET",
		fmt.Sprintf("%sintelligent/search/terminate?id=%s", apiURL, searchID), nil)
	if err != nil {
		return
	}
	req.Header.Set("x-key", apiKey)

	resp, err := session.Client.Do(req)
	if err != nil {
		return
	}
	_ = ctx
	session.DiscardHTTPResponse(resp)
}

func (s *IntelX) formatRecord(record intelxResultRecord) string {
	var parts []string
	if record.Name != "" {
		parts = append(parts, "name:"+record.Name)
	}
	if record.MediaH != "" {
		parts = append(parts, "type:"+record.MediaH)
	}
	if record.Bucket != "" {
		parts = append(parts, "bucket:"+record.Bucket)
	}
	if record.Date != "" {
		parts = append(parts, "date:"+record.Date)
	}
	return strings.Join(parts, ", ")
}

func (s *IntelX) Name() string {
	return "intelx"
}

func (s *IntelX) IsDefault() bool {
	return false
}

func (s *IntelX) NeedsKey() bool {
	return true
}

func (s *IntelX) AddApiKeys(keys []string) {
	for _, key := range keys {
		idx := strings.Index(key, ":")
		if idx < 0 {
			logger.Warnf("IntelX: invalid key format %q — expected HOST:API_KEY (e.g. 2.intelx.io:your-uuid-key)", key)
			continue
		}
		s.apiKeys = append(s.apiKeys, intelxKey{
			host:   key[:idx],
			apiKey: key[idx+1:],
		})
	}
}

func (s *IntelX) RateLimit() int {
	return 1
}
