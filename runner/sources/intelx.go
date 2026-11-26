package sources

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type IntelX struct {
	apiKeys []intelxKey

	// pollDelay overrides MinInterval between result polls (tests only).
	pollDelay time.Duration
}

// intelxKey holds a parsed HOST:API_KEY pair.
type intelxKey struct {
	host   string // e.g. "2.intelx.io" or "free.intelx.io"
	apiKey string // UUID API key
}

// intelxSearchRequest is the request body for POST /intelligent/search
type intelxSearchRequest struct {
	Term       string   `json:"term"`
	Buckets    []string `json:"buckets"`
	MaxResults int      `json:"maxresults"`
	Sort       int      `json:"sort"` // 4 = date desc
	Media      int      `json:"media"`
}

// intelxLeakBuckets restricts searches to leak/paste/darknet buckets only.
// This prevents downloading and parsing unrelated web pages that merely mention the search term.
var intelxLeakBuckets = []string{
	"leaks.public",
	"leaks.private",
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

// intelxMaxRecords is the most records a search returns per bucket on paid
// keys (free keys get 200).
const intelxMaxRecords = 1000

// intelxDefaultReads is the files read per target without --max-requests.
const intelxDefaultReads = 10

// maxFileReadSize is the maximum number of bytes to read from a single file.
// This prevents downloading very large files.
const maxFileReadSize = 10 * 1024 * 1024 // 10 MB

func (s *IntelX) Search(ctx context.Context, q Query, session *Session, emit func(Result)) (Stats, error) {
	stats := newStats()
	key := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
	if key.apiKey == "" {
		return stats, nil
	}
	target := q.Target
	randomApiKey := key.apiKey
	apiURL := fmt.Sprintf("https://%s/", key.host)
	lowerTarget := strings.ToLower(target)

	// Records are files; their matching lines are the results. The search
	// asks for as many records as results are wanted, up to the API's cap.
	maxRecords := newPager(q, s.Limits()).limit()
	lines := newPager(q, Limits{})

	// Start the search, restricted to leak/paste/darknet buckets only.
	// Without bucket filtering, IntelX returns all indexed content including
	// web pages that merely mention the domain, producing garbage results.
	searchReq := intelxSearchRequest{
		Term:       target,
		Buckets:    intelxLeakBuckets,
		MaxResults: maxRecords,
		Sort:       4, // date desc
		Media:      0, // all media types
	}

	body, err := json.Marshal(searchReq)
	if err != nil {
		return stats, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"intelligent/search", bytes.NewReader(body))
	if err != nil {
		return stats, err
	}
	req.Header.Set("x-key", randomApiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending search request in IntelX source for %s", target)
	resp, err := session.Do(ctx, req)
	if err != nil {
		return stats, err
	}
	respBody, err := io.ReadAll(resp.Body)
	session.DiscardHTTPResponse(resp)
	if err != nil {
		return stats, err
	}

	var searchResp intelxSearchResponse
	if err := json.Unmarshal(respBody, &searchResp); err != nil {
		return stats, err
	}
	if searchResp.Status != 0 {
		return stats, fmt.Errorf("IntelX search failed with status %d", searchResp.Status)
	}

	searchID := searchResp.ID
	logger.Debugf("IntelX search started with ID %s", searchID)
	// Free the search on the server once polling is over, or on any early return.
	terminate := sync.OnceFunc(func() { s.terminateSearch(session, apiURL, randomApiKey, searchID) })
	defer terminate()

	// Collect all records first, then fetch file contents
	var allRecords []intelxResultRecord

	// Poll for results (up to 10 attempts, pollInterval apart)
	for attempt := 0; attempt < 10; attempt++ {
		select {
		case <-ctx.Done():
			return stats, ctx.Err()
		case <-time.After(s.pollInterval()):
		}

		logger.Debugf("Sending a request for IntelX poll attempt %d", attempt)

		resultReq, err := http.NewRequestWithContext(ctx, "GET",
			fmt.Sprintf("%sintelligent/search/result?id=%s&limit=100", apiURL, searchID), nil)
		if err != nil {
			return stats, err
		}
		resultReq.Header.Set("x-key", randomApiKey)
		resultReq.Header.Set("Accept", "application/json")

		resultResp, err := session.Do(ctx, resultReq)
		if err != nil {
			return stats, err
		}
		resultBody, err := io.ReadAll(resultResp.Body)
		session.DiscardHTTPResponse(resultResp)
		if err != nil {
			return stats, err
		}

		var resultData intelxResultResponse
		if err := json.Unmarshal(resultBody, &resultData); err != nil {
			return stats, err
		}

		logger.Debugf("IntelX poll attempt %d: status=%d, records=%d", attempt, resultData.Status, len(resultData.Records))

		allRecords = append(allRecords, resultData.Records...)

		// Status: 0=continue, 1=done, 2=not found, 3=keep trying
		if resultData.Status == 1 || resultData.Status == 2 {
			break
		}
	}

	terminate()
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

	// Filter out records from non-leak buckets as a safety net.
	// Even with bucket filtering in the search request, this guards against
	// unexpected results (e.g. if the API ignores unknown bucket names).
	var filteredRecords []intelxResultRecord
	for _, record := range uniqueRecords {
		if isIntelxWebBucket(record.Bucket) {
			logger.Debugf("IntelX skipping record %s from web bucket %q", record.Name, record.Bucket)
			continue
		}
		filteredRecords = append(filteredRecords, record)
	}

	logger.Debugf("IntelX %d records after bucket filtering (was %d)", len(filteredRecords), len(uniqueRecords))

	// For each filtered record, fetch file contents and extract matching
	// lines. Every read costs a read credit, so --max-requests caps the
	// reads. A 402 means the read budget is spent: stop reading.
	reads := 0
	for _, record := range filteredRecords {
		if err := ctx.Err(); err != nil {
			return lines.stats(), err
		}
		if lines.want() == 0 {
			return intelxTruncated(lines, LimitResults, reads, len(filteredRecords)), nil
		}
		if q.MaxRequests > 0 && reads >= q.MaxRequests {
			return intelxTruncated(lines, LimitRequests, reads, len(filteredRecords)), nil
		}
		matches, status, readErr := s.fetchMatchingLines(ctx, session, apiURL, randomApiKey, record, lowerTarget)
		reads++
		if status == http.StatusPaymentRequired {
			return lines.stats(), withMessage(ErrQuota, "IntelX file read quota exhausted (status 402); stopped reading files")
		}
		var results []Result
		for _, line := range matches {
			r := parseIntelxLine(line, lowerTarget)
			r.Source = s.Name()
			if r.HasData() {
				results = append(results, r)
			}
		}
		for _, r := range results[:lines.take(len(results))] {
			emit(r)
		}
		if readErr != nil && ctx.Err() == nil {
			// One unreadable file does not fail the search.
			logger.Warnf("IntelX: %s", readErr)
		}
	}
	// As many records as asked for: the search may have had more.
	if len(allRecords) >= maxRecords {
		reason := LimitResults
		if maxRecords == intelxMaxRecords {
			reason = LimitAPI
		}
		return intelxTruncated(lines, reason, reads, len(filteredRecords)), nil
	}
	return lines.stats(), nil
}

// intelxTruncated returns the stats of a search that left records unread,
// with how many of the files were read.
func intelxTruncated(lines *pager, reason Limit, read, files int) Stats {
	stats := lines.stats()
	stats.Truncated, stats.Limit = true, reason
	stats.Note = fmt.Sprintf("from %d of %d files", read, files)
	return stats
}

// parseIntelxLine parses a raw leak line from IntelX files.
// Lines come in various formats:
//   - URL:LOGIN:PASSWORD (stealer logs)
//   - email:password (combo list), also with ; tab | or space between fields
//   - CSV: id,,email,hash,name (database dumps), fields possibly quoted
//   - email:password:hash:salt
//
// The parser detects the format and maps fields to Result.
func parseIntelxLine(line, target string) Result {
	var r Result
	line = strings.TrimSpace(line)
	if line == "" {
		return r
	}

	if u, ok := parseIntelxURLLine(line, target); ok {
		return u
	}

	// Try CSV format first (comma-separated with potential empty fields)
	if strings.Count(line, ",") >= 2 {
		parts := strings.Split(line, ",")
		for i := range parts {
			parts[i] = unquoteIntelx(parts[i])
		}
		// Find which part contains the target email
		emailIdx := -1
		for i, p := range parts {
			if strings.Contains(strings.ToLower(p), target) && strings.Contains(p, "@") {
				emailIdx = i
				break
			}
		}
		if emailIdx >= 0 {
			r.Email = parts[emailIdx]
			// Assign remaining non-empty fields heuristically
			for i, p := range parts {
				if i == emailIdx || p == "" {
					continue
				}
				assignIntelxField(&r, p)
			}
			return r
		}
	}

	// Colon-separated: email:password or email:password:hash:extra. Other
	// dumps put a semicolon, tab, pipe or spaces between the fields.
	var parts []string
	switch {
	case strings.Contains(line, ":"):
		parts = strings.SplitN(line, ":", 4)
	case strings.Contains(line, ";"):
		parts = strings.SplitN(line, ";", 4)
	case strings.ContainsAny(line, "\t|"):
		parts = strings.FieldsFunc(line, func(c rune) bool { return c == '\t' || c == '|' })
	default:
		parts = strings.Fields(line)
	}
	for i := range parts {
		parts[i] = unquoteIntelx(parts[i])
	}
	switch len(parts) {
	case 1:
		r.Email = parts[0]
	case 2:
		r.Email = parts[0]
		r.Password = parts[1]
	case 3:
		r.Email = parts[0]
		r.Password = parts[1]
		// Third field is typically a hash
		if looksLikeHash(parts[2]) {
			r.Hash = parts[2]
		} else if parts[2] != "" {
			r.SetExtra("field3", parts[2])
		}
	default: // 4+
		r.Email = parts[0]
		r.Password = parts[1]
		if looksLikeHash(parts[2]) {
			r.Hash = parts[2]
		} else if parts[2] != "" {
			r.SetExtra("field3", parts[2])
		}
		if parts[3] != "" {
			r.Salt = parts[3]
		}
	}

	return r
}

// parseIntelxURLLine parses a URL:LOGIN:PASSWORD line from stealer logs. The
// URL keeps its scheme and port: "https://login.example.com:8443/x:john:pw".
// A line without a scheme is only read this way when its host belongs to the
// searched domain, so "user.name:password:hash" combos keep their meaning.
func parseIntelxURLLine(line, target string) (Result, bool) {
	last := strings.LastIndex(line, ":")
	if last <= 0 {
		return Result{}, false
	}
	prev := strings.LastIndex(line[:last], ":")
	if prev <= 0 {
		return Result{}, false
	}
	pageURL, login, password := line[:prev], line[prev+1:last], line[last+1:]
	if login == "" {
		return Result{}, false
	}
	if strings.Contains(pageURL, "://") {
		if urlHost(pageURL) == "" {
			return Result{}, false
		}
	} else if strings.ContainsAny(pageURL, "@ ") || !withinDomain(urlHost(pageURL), normalizeHost(target)) {
		return Result{}, false
	}

	r := Result{URL: pageURL, Password: password}
	if strings.Contains(login, "@") {
		r.Email = login
	} else {
		r.Username = login
	}
	return r, true
}

// unquoteIntelx trims spaces and one pair of surrounding quotes from a field.
func unquoteIntelx(field string) string {
	field = strings.TrimSpace(field)
	if len(field) >= 2 {
		first, last := field[0], field[len(field)-1]
		if first == last && (first == '\'' || first == '"' || first == '`') {
			field = strings.TrimSpace(field[1 : len(field)-1])
		}
	}
	return field
}

// assignIntelxField heuristically assigns a CSV field value to a Result.
func assignIntelxField(r *Result, val string) {
	if looksLikeHash(val) {
		if r.Hash == "" {
			r.Hash = val
		}
		return
	}
	// Numeric-only → could be user ID, skip unless it looks like a phone
	isNumeric := true
	for _, c := range val {
		if c < '0' || c > '9' {
			isNumeric = false
			break
		}
	}
	if isNumeric {
		return // skip numeric IDs
	}
	// Otherwise treat as a name/label
	if r.Name == "" {
		r.Name = val
	} else {
		r.SetExtra("intelx_field", val)
	}
}

// looksLikeHash returns true if the string looks like a hash (hex, base64, or prefixed).
func looksLikeHash(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Prefixed hashes like 0x..., $2a$..., $H$...
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "$") {
		return true
	}
	// Hex hash (32+ chars, all hex)
	if len(s) >= 32 {
		allHex := true
		for _, c := range s {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				allHex = false
				break
			}
		}
		if allHex {
			return true
		}
	}
	// Base64-encoded hash (ends with = or ==, 20+ chars)
	if len(s) >= 20 && (strings.HasSuffix(s, "=") || strings.HasSuffix(s, "==")) {
		return true
	}
	return false
}

// fetchMatchingLines reads a file from IntelX and returns lines containing the target.
// For redacted files (free tier), the content will be masked and won't match the target,
// which is the correct behavior — downstream filtering handles this naturally.
func (s *IntelX) fetchMatchingLines(ctx context.Context, session *Session, apiURL, apiKey string, record intelxResultRecord, target string) ([]string, int, error) {
	readURL := fmt.Sprintf("%sfile/read?type=%d&limit=0", apiURL, record.Type)

	logger.Debugf("Sending a request in IntelX source for file %s", record.Name)

	req, err := http.NewRequestWithContext(ctx, "GET", readURL, nil)
	if err != nil {
		logger.Debugf("IntelX file read request error for %s: %v", record.Name, err)
		return nil, 0, fmt.Errorf("IntelX file read request could not be created")
	}
	// Set query params via url.Values to ensure proper encoding
	q := req.URL.Query()
	q.Set("storageid", record.StorageID)
	q.Set("bucket", record.Bucket)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("x-key", apiKey)

	resp, err := session.Do(ctx, req)
	if err != nil {
		var statusErr *StatusError
		if errors.As(err, &statusErr) {
			logger.Debugf("IntelX file read returned status %d for %s", statusErr.StatusCode, record.Name)
			return nil, statusErr.StatusCode, nil
		}
		logger.Debugf("IntelX file read error for %s: %v", record.Name, err)
		return nil, 0, fmt.Errorf("IntelX file read request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	// Read one byte past the cap to distinguish a complete file from a truncated
	// one, then close the response without draining any remaining content.
	limitedReader := &io.LimitedReader{R: resp.Body, N: maxFileReadSize + 1}
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
	if err := scanner.Err(); err != nil {
		logger.Debugf("IntelX file scan error for %s: %v", record.Name, err)
		return matches, http.StatusOK, fmt.Errorf("IntelX file scan failed: %w", err)
	}
	if limitedReader.N == 0 {
		err := fmt.Errorf("IntelX file exceeded the %d-byte read limit", maxFileReadSize)
		logger.Debugf("IntelX file scan error for %s: %v", record.Name, err)
		return matches, http.StatusOK, err
	}

	if len(matches) > 0 {
		logger.Debugf("IntelX file %s: found %d matching lines", record.Name, len(matches))
	}

	return matches, http.StatusOK, nil
}

// isIntelxWebBucket returns true if the bucket contains web-crawled content
// rather than leaked credentials or paste data.
func isIntelxWebBucket(bucket string) bool {
	return strings.HasPrefix(bucket, "web.") ||
		strings.HasPrefix(bucket, "news.") ||
		strings.HasPrefix(bucket, "government.") ||
		strings.HasPrefix(bucket, "whois.")
}

// terminateSearch frees a search on the server. It runs on its own short
// context, so it still runs when the search was interrupted.
func (s *IntelX) terminateSearch(session *Session, apiURL, apiKey, searchID string) {
	terminateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(terminateCtx, "GET",
		fmt.Sprintf("%sintelligent/search/terminate?id=%s", apiURL, searchID), nil)
	if err != nil {
		return
	}
	req.Header.Set("x-key", apiKey)

	resp, err := session.Do(terminateCtx, req)
	if err != nil {
		return
	}
	session.DiscardHTTPResponse(resp)
}

// Limits: result pages of 100 records; up to 1,000 records per bucket on
// paid keys (200 on free ones). Every file read is a request: without
// --max-requests, IntelX reads up to intelxDefaultReads files per target.
func (s *IntelX) Limits() Limits {
	return Limits{PageSize: 100, MaxResults: intelxMaxRecords, DefaultRequests: intelxDefaultReads}
}

func (s *IntelX) Name() string {
	return "intelx"
}

func (s *IntelX) UsesKey() bool {
	return true
}

func (s *IntelX) NeedsKey() bool {
	return true
}

func (s *IntelX) AddApiKeys(keys []string) {
	s.apiKeys = nil
	for _, key := range keys {
		idx := strings.Index(key, ":")
		if idx <= 0 || idx == len(key)-1 {
			// Never print the key itself.
			logger.Warnf("IntelX: ignoring a key that is not in HOST:API_KEY format (e.g. 2.intelx.io:your-uuid-key)")
			continue
		}
		s.apiKeys = append(s.apiKeys, intelxKey{
			host:   key[:idx],
			apiKey: key[idx+1:],
		})
	}
}

// Types: IntelX searches free text, so every type is searched as text.
func (s *IntelX) Types() []ScanType { return AllTypes }

// HasKeys reports whether a usable API key is configured.
func (s *IntelX) HasKeys() bool {
	return len(s.apiKeys) > 0
}

// MinInterval returns the minimum time between two requests to the source.
func (s *IntelX) MinInterval() time.Duration {
	return time.Second
}

// pollInterval is the pause before each result poll.
func (s *IntelX) pollInterval() time.Duration {
	if s.pollDelay > 0 {
		return s.pollDelay
	}
	return s.MinInterval()
}
