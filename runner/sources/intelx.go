package sources

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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
	StorageID   string `json:"storageid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MediaH      string `json:"mediah"`
	Date        string `json:"date"`
	Bucket      string `json:"bucket"`
	AccessLevel int    `json:"accesslevel"`
	Type        int    `json:"type"`
}

type intelxResultResponse struct {
	Records []intelxResultRecord `json:"records"`
	Status  int                  `json:"status"` // 0=results(continue), 1=no more, 2=not found, 3=keep trying
}

// maxFileReadSize is the maximum number of bytes to read from a single file.
// This prevents downloading very large files.
const maxFileReadSize = 10 * 1024 * 1024 // 10 MB

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
		lowerTarget := strings.ToLower(target)

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

		// Collect all records first, then fetch file contents
		var allRecords []intelxResultRecord

		// Poll for results (up to 10 attempts with 2s delay)
		for attempt := 0; attempt < 10; attempt++ {
			select {
			case <-ctx.Done():
				s.terminateSearch(ctx, session, apiURL, randomApiKey, searchID)
				return
			case <-time.After(2 * time.Second):
			}

			resultReq, err := http.NewRequestWithContext(ctx, "GET",
				fmt.Sprintf("%sintelligent/search/result?id=%s&limit=100", apiURL, searchID), nil)
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

			logger.Debugf("IntelX poll attempt %d: status=%d, records=%d", attempt, resultData.Status, len(resultData.Records))

			allRecords = append(allRecords, resultData.Records...)

			// Status: 0=continue, 1=done, 2=not found, 3=keep trying
			if resultData.Status == 1 || resultData.Status == 2 {
				break
			}
			if resultData.Status == 0 {
				continue
			}
			// Status 3: no results yet, keep trying
		}

		// Terminate search to free resources
		s.terminateSearch(ctx, session, apiURL, randomApiKey, searchID)

		logger.Debugf("IntelX found %d records, fetching file contents", len(allRecords))

		// Sort records by access level (public first) so we prefer readable files
		sort.Slice(allRecords, func(i, j int) bool {
			return allRecords[i].AccessLevel < allRecords[j].AccessLevel
		})

		// Deduplicate records by StorageID (same file can appear with different part markers)
		seen := make(map[string]struct{})
		var uniqueRecords []intelxResultRecord
		for _, record := range allRecords {
			if _, ok := seen[record.StorageID]; ok {
				continue
			}
			seen[record.StorageID] = struct{}{}
			uniqueRecords = append(uniqueRecords, record)
		}

		// For each unique record, fetch file contents and extract matching lines.
		// Stop fetching if we hit a rate limit (402) — the API budget is exhausted.
		rateLimited := false
		for _, record := range uniqueRecords {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if rateLimited {
				break
			}

			lines, status := s.fetchMatchingLines(ctx, session, apiURL, randomApiKey, record, lowerTarget)
			if status == http.StatusPaymentRequired {
				rateLimited = true
				logger.Debugf("IntelX file read rate limited (402), stopping further reads")
				continue
			}
			for _, line := range lines {
				results <- Result{Source: s.Name(), Value: line}
			}
		}
	}()

	return results
}

// fetchMatchingLines reads a file from IntelX and returns lines containing the target.
// For redacted files (free tier), the content will be masked and won't match the target,
// which is the correct behavior — downstream filtering handles this naturally.
func (s *IntelX) fetchMatchingLines(ctx context.Context, session *Session, apiURL, apiKey string, record intelxResultRecord, target string) ([]string, int) {
	readURL := fmt.Sprintf("%sfile/read?type=%d&limit=0", apiURL, record.Type)

	req, err := http.NewRequestWithContext(ctx, "GET", readURL, nil)
	if err != nil {
		logger.Debugf("IntelX file read request error for %s: %v", record.Name, err)
		return nil, 0
	}
	// Set query params via url.Values to ensure proper encoding
	q := req.URL.Query()
	q.Set("storageid", record.StorageID)
	q.Set("bucket", record.Bucket)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("x-key", apiKey)

	resp, err := session.Client.Do(req)
	if err != nil {
		logger.Debugf("IntelX file read error for %s: %v", record.Name, err)
		return nil, 0
	}
	defer session.DiscardHTTPResponse(resp)

	if resp.StatusCode != http.StatusOK {
		logger.Debugf("IntelX file read returned status %d for %s", resp.StatusCode, record.Name)
		return nil, resp.StatusCode
	}

	// Read the response body with a size limit
	limitedReader := io.LimitReader(resp.Body, maxFileReadSize)
	scanner := bufio.NewScanner(limitedReader)
	// Increase scanner buffer for potentially long lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var matches []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), target) {
			matches = append(matches, line)
		}
	}

	if len(matches) > 0 {
		logger.Debugf("IntelX file %s: found %d matching lines", record.Name, len(matches))
	}

	return matches, http.StatusOK
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

func (s *IntelX) Name() string {
	return "intelx"
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
