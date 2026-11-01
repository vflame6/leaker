package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

// Shared code for the two DarkEye sources (darkeye.io), each with its own key
// and its own row in the provider config:
//
//   - HIBR (hibr.go): Have I Been Ransomed? (haveibeenransom.com), ransomware
//     leak-site posts and data breaches for a domain.
//   - BreachHouse (breachhouse.go): Breach House (breach.house), ransomware
//     victims, data breaches, leads and stealer records matching a domain.
//
// Only listing metadata is returned (title, website, group, country, date);
// leaked records, proof files and leak-site or download URLs are never read
// from the responses.

const (
	// Upper bound for a response body, as a guard against unexpected payloads.
	darkEyeMaxBodySize = 8 << 20
	// Upper bound for a single metadata value.
	darkEyeMaxFieldRunes = 200
)

// The APIs treat * and ? as wildcards, so only plain domain names are queried.
var darkEyeDomainRegex = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// darkEyeText is a metadata value that may arrive as a JSON string or number.
// Any other JSON type decodes to an empty value instead of failing the response.
type darkEyeText string

func (t *darkEyeText) UnmarshalJSON(data []byte) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch val := v.(type) {
	case string:
		*t = darkEyeText(val)
	case float64:
		*t = darkEyeText(strconv.FormatFloat(val, 'f', -1, 64))
	default:
		*t = ""
	}
	return nil
}

// darkEyeItem holds the only fields read from a HIBR or Breach House record.
type darkEyeItem struct {
	IDSource   darkEyeText `json:"id_source"`
	ID         darkEyeText `json:"id"`
	VictimID   darkEyeText `json:"victim_id"`
	LeakType   darkEyeText `json:"leak_type"`
	PostTitle  darkEyeText `json:"post_title"`
	Name       darkEyeText `json:"name"`
	Website    darkEyeText `json:"website"`
	GroupName  darkEyeText `json:"group_name"`
	Country    darkEyeText `json:"country"`
	Discovered darkEyeText `json:"discovered"`
}

// darkEyeRecord is a parsed record together with its category.
type darkEyeRecord struct {
	category string
	id       string
	item     darkEyeItem
}

// darkEyeLookup describes one DarkEye source for darkEyeRun.
type darkEyeLookup struct {
	source  string // source name, e.g. "hibr"
	product string // product name used in messages
	keys    []string
	// keyedByDomain is true when the API is keyed by domain, so every record
	// belongs to the searched domain even without a website of its own.
	keyedByDomain bool
	fetch         func(ctx context.Context, apiKey, domain string) ([]darkEyeRecord, error)
}

// darkEyeRun runs one DarkEye lookup. Records read before a failure are still
// returned, followed by the failure as an error result.
func darkEyeRun(ctx context.Context, l darkEyeLookup, target string, scanType ScanType) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		// DarkEye sources are searched by domain only.
		if scanType != TypeDomain {
			return
		}

		apiKey := utils.PickRandom(l.keys, l.source, true)
		if apiKey == "" {
			return
		}

		domain, ok := normalizeDarkEyeDomain(target)
		if !ok {
			send(ctx, results, Result{Source: l.source, Error: fmt.Errorf("%s needs a plain domain name, got %q", l.product, target)})
			return
		}

		records, err := l.fetch(ctx, apiKey, domain)
		for _, record := range records {
			r, ok := darkEyeRecordToResult(l.source, l.keyedByDomain, record, domain)
			if !ok {
				continue
			}
			if !send(ctx, results, r) {
				return
			}
		}
		if err != nil && ctx.Err() == nil {
			send(ctx, results, Result{Source: l.source, Error: err})
		}
	}()

	return results
}

// send delivers a result unless the context is cancelled first.
func send(ctx context.Context, results chan<- Result, r Result) bool {
	select {
	case results <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

// darkEyePause waits between two requests to respect the rate limit.
func darkEyePause(ctx context.Context, rateLimit int, noDelay bool) bool {
	if noDelay {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(time.Second / time.Duration(rateLimit)):
		return true
	}
}

// darkEyeRedact removes every configured API key from a message.
func darkEyeRedact(msg string, keys []string) string {
	for _, key := range keys {
		msg = strings.ReplaceAll(msg, key, "[redacted]")
	}
	return msg
}

// darkEyeGet performs an authenticated GET request and returns the response body.
// Errors never include the API key or the response body.
func darkEyeGet(ctx context.Context, session *Session, product, endpoint, apiKey string, keys []string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%s request could not be created", product)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request to %s", product)
	resp, err := session.Client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%s request failed: %s", product, darkEyeRedact(err.Error(), keys))
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(io.LimitReader(resp.Body, darkEyeMaxBodySize))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%s response could not be read", product)
	}
	logger.Debugf("Response from %s: status code [%d], size [%d]", product, resp.StatusCode, len(body))

	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%s authentication failed (status %d), check its API key", product, resp.StatusCode)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("%s quota or rate limit reached (status %d), retry later", product, resp.StatusCode)
	default:
		return nil, fmt.Errorf("%s returned status %d", product, resp.StatusCode)
	}
}

// darkEyeRecordToResult maps a record to a Result that carries listing metadata only.
//
// Results that do not mention the searched domain are dropped by the default
// result filter. The HIBR endpoint is keyed by domain, so its records are tied to
// the domain even when the record has no website of its own: URL then holds the
// searched domain. Breach House runs a free-text search, so its records keep their
// own website and are filtered like any other result.
func darkEyeRecordToResult(source string, keyedByDomain bool, record darkEyeRecord, domain string) (Result, bool) {
	item := record.item

	website := darkEyeClean(string(item.Website))
	title := darkEyeClean(string(item.PostTitle))
	if title == "" {
		title = darkEyeClean(string(item.Name))
	}
	if title == "" {
		title = website
	}

	r := Result{
		Source:   source,
		Database: title,
		URL:      website,
	}
	if keyedByDomain && !strings.Contains(strings.ToLower(website), domain) {
		r.URL = domain
		if website != "" {
			r.SetExtra("website", website)
		}
	}
	if r.Database == "" && r.URL == "" {
		return Result{}, false
	}

	if record.category != "" {
		r.SetExtra("category", record.category)
	}
	if v := darkEyeClean(string(item.GroupName)); v != "" {
		r.SetExtra("group", v)
	}
	if v := darkEyeClean(string(item.Country)); v != "" {
		r.SetExtra("country", v)
	}
	if v := darkEyeDate(string(item.Discovered)); v != "" {
		r.SetExtra("date", v)
	}
	// The listing id keeps two records of the same day and group apart in the
	// result deduplication, which ignores the Database field.
	if v := darkEyeClean(record.id); v != "" {
		r.SetExtra("record_id", v)
	}
	return r, true
}

// normalizeDarkEyeDomain returns the lowercase plain domain name for target.
// Wildcards, URLs, IP addresses and other garbage are rejected.
func normalizeDarkEyeDomain(target string) (string, bool) {
	domain := strings.TrimRight(strings.ToLower(strings.TrimSpace(target)), ".")
	if len(domain) > 253 || !darkEyeDomainRegex.MatchString(domain) {
		return "", false
	}
	// An IP address matches the pattern above; a TLD is never numeric.
	tld := domain[strings.LastIndex(domain, ".")+1:]
	if _, err := strconv.Atoi(tld); err == nil {
		return "", false
	}
	return domain, true
}

// darkEyeClean returns a single-line, printable, length-limited copy of a value.
// Titles come from third-party leak sites, so control characters (for example
// terminal escape sequences) are removed before they reach the output.
func darkEyeClean(value string) string {
	value = strings.ToValidUTF8(value, "")
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) > darkEyeMaxFieldRunes {
		value = string([]rune(value)[:darkEyeMaxFieldRunes])
	}
	return value
}

// darkEyeDate keeps the YYYY-MM-DD part of a timestamp, or "" when it is not a date.
func darkEyeDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < len("2006-01-02") {
		return ""
	}
	day := value[:len("2006-01-02")]
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return ""
	}
	return day
}
