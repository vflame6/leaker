package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Query is one search: a target, its type and the limits of the run.
type Query struct {
	Target string
	Type   ScanType
	// MaxResults is the number of records to fetch at most; 0 means as many
	// as the API returns.
	MaxResults int
	// MaxRequests is the number of search requests (pages) to send at most;
	// 0 means no limit.
	MaxRequests int
}

// Stats is what a source reports about one search.
type Stats struct {
	// Requests is the number of HTTP requests sent, retries included. The
	// runner fills it from the source's session.
	Requests int
	// Fetched is the number of records the source emitted. The runner
	// fills it.
	Fetched int
	// Total is the number of records the API says exist; -1 if unknown.
	Total int
	// Truncated is true when records were left behind; Limit says why.
	Truncated bool
	Limit     Limit
	// CreditsLeft is the balance the API reports; nil when it reports none.
	CreditsLeft *int
	// CreditsUsed is what this search cost, when the API says; nil otherwise.
	CreditsUsed *int
	// Note details a truncation for the notice, e.g. "from 10 of 57 files".
	Note string
}

// Limit is the cap that stopped a truncated search.
type Limit int

const (
	LimitNone     Limit = iota
	LimitResults        // --max-results
	LimitRequests       // --max-requests
	LimitAPI            // the API returns no more per search
	LimitSafety         // MaxPagesWithoutLimit reached without --max-requests
)

// Source is one leak database. Search runs synchronously: it calls emit for
// every record, from the goroutine that called Search, and returns what it
// knows about the search. Errors are returned, never emitted.
type Source interface {
	Search(ctx context.Context, q Query, s *Session, emit func(Result)) (Stats, error)

	// Types returns the scan types the API can search. The runner skips the
	// source for any other type.
	Types() []ScanType

	// Limits returns the page size and the cap of one search.
	Limits() Limits

	// Name returns the name of the source. It is preferred to use lower case names.
	Name() string

	// UsesKey returns true if the source supports an API key
	UsesKey() bool

	// NeedsKey returns true if the source requires an API key
	NeedsKey() bool

	AddApiKeys([]string)

	// HasKeys reports whether a usable API key is configured
	HasKeys() bool

	// MinInterval returns the minimum time between two requests to the source
	MinInterval() time.Duration
}

// Result represents a single leak result from a source.
type Result struct {
	Source   string
	Email    string
	Username string
	Password string
	Hash     string
	Salt     string
	IP       string
	Phone    string
	Name     string
	Database string
	URL      string
	Extra    map[string]string
	Error    error

	// cachedChecksum stores the lazily computed SHA-256 hex digest of the
	// canonical leak fields. Populated on first Checksum() call, or directly
	// by LeakerDB.Search when reconstructing a row (which already knows the
	// checksum from the DB primary key).
	cachedChecksum string
}

// TrimSpaces strips leading and trailing whitespace from every string field
// on the Result except Password and Salt, whose whitespace is considered
// meaningful (leak dumps sometimes include passwords that intentionally
// contain leading/trailing spaces). Extra map values are trimmed as well;
// keys are left untouched because they are developer-defined identifiers.
//
// Fields that collapse to an empty string after trimming are left empty,
// so the downstream HasData() check correctly drops results whose only
// "content" was whitespace.
func (r *Result) TrimSpaces() {
	r.Source = strings.TrimSpace(r.Source)
	r.Email = strings.TrimSpace(r.Email)
	r.Username = strings.TrimSpace(r.Username)
	r.Hash = strings.TrimSpace(r.Hash)
	r.IP = strings.TrimSpace(r.IP)
	r.Phone = strings.TrimSpace(r.Phone)
	r.Name = strings.TrimSpace(r.Name)
	r.Database = strings.TrimSpace(r.Database)
	r.URL = strings.TrimSpace(r.URL)
	for k, v := range r.Extra {
		r.Extra[k] = strings.TrimSpace(v)
	}
}

// SetExtra sets a key-value pair in the Extra map, initializing it if needed.
func (r *Result) SetExtra(key, value string) {
	if r.Extra == nil {
		r.Extra = make(map[string]string)
	}
	r.Extra[key] = value
}

// Value returns a formatted "field:value, field:value" string for display.
// Fields are emitted in a fixed order for consistency.
// Database is excluded — use MetadataValue() to include it.
func (r *Result) Value() string {
	return r.formatValue(false)
}

// MetadataValue returns Value() with metadata fields (Database) included.
func (r *Result) MetadataValue() string {
	return r.formatValue(true)
}

func (r *Result) formatValue(includeDatabase bool) string {
	var parts []string

	if r.Email != "" {
		parts = append(parts, "email:"+r.Email)
	}
	if r.Username != "" {
		parts = append(parts, "username:"+r.Username)
	}
	if r.Password != "" {
		parts = append(parts, "password:"+r.Password)
	}
	if r.Hash != "" {
		parts = append(parts, "hash:"+r.Hash)
	}
	if r.Salt != "" {
		parts = append(parts, "salt:"+r.Salt)
	}
	if r.IP != "" {
		parts = append(parts, "ip:"+r.IP)
	}
	if r.Phone != "" {
		parts = append(parts, "phone:"+r.Phone)
	}
	if r.Name != "" {
		parts = append(parts, "name:"+r.Name)
	}
	if includeDatabase && r.Database != "" {
		parts = append(parts, "database:"+r.Database)
	}
	if r.URL != "" {
		parts = append(parts, "url:"+r.URL)
	}
	// Extra keys are sorted so the same record always prints the same way.
	keys := make([]string, 0, len(r.Extra))
	for k := range r.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+":"+r.Extra[k])
	}

	return strings.Join(parts, ", ")
}

// Checksum returns a stable SHA-256 hex digest over the canonical leak fields.
// It excludes Database (so two records that differ only by source DB collapse
// to one) and excludes Source/Error (which are not part of the leak content).
// Extra is included with sorted keys so the encoding is deterministic.
//
// The first call computes and caches the digest on the Result; subsequent
// calls return the cached value, even if fields are mutated afterwards.
func (r *Result) Checksum() string {
	if r.cachedChecksum != "" {
		return r.cachedChecksum
	}
	r.cachedChecksum = computeChecksum(r)
	return r.cachedChecksum
}

// SetCachedChecksum overrides the cached checksum without recomputing it.
// Used by LeakerDB.Search when reconstructing a Result from a DB row where
// the checksum is already known (it's the primary key of the row).
func (r *Result) SetCachedChecksum(sum string) {
	r.cachedChecksum = sum
}

// computeChecksum builds the canonical pipe-separated field string and
// returns its lowercase SHA-256 hex digest.
func computeChecksum(r *Result) string {
	var b strings.Builder
	b.WriteString("email:")
	b.WriteString(r.Email)
	b.WriteString("|username:")
	b.WriteString(r.Username)
	b.WriteString("|password:")
	b.WriteString(r.Password)
	b.WriteString("|hash:")
	b.WriteString(r.Hash)
	b.WriteString("|salt:")
	b.WriteString(r.Salt)
	b.WriteString("|ip:")
	b.WriteString(r.IP)
	b.WriteString("|phone:")
	b.WriteString(r.Phone)
	b.WriteString("|name:")
	b.WriteString(r.Name)
	b.WriteString("|url:")
	b.WriteString(r.URL)

	// Sort Extra keys for deterministic encoding.
	if len(r.Extra) > 0 {
		keys := make([]string, 0, len(r.Extra))
		for k := range r.Extra {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString("|")
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(r.Extra[k])
		}
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// HasData returns true if the result contains at least one data field.
func (r *Result) HasData() bool {
	return r.Email != "" || r.Username != "" || r.Password != "" ||
		r.Hash != "" || r.Salt != "" || r.IP != "" || r.Phone != "" ||
		r.Name != "" || r.Database != "" || r.URL != "" ||
		len(r.Extra) > 0
}

// CustomTransport wraps http.Transport and adds a default User-Agent header.
type CustomTransport struct {
	Transport http.RoundTripper
	UserAgent string
}

// Session is the HTTP session of a run. Every request of every source goes
// through Do; ForSource gives each source its own rate limit and counter.
type Session struct {
	client *http.Client
	retry  RetryPolicy
	// pace spaces the requests of one source; nil means no wait.
	pace *pacer
	// sent counts the requests sent through this handle.
	sent *atomic.Int64
}

// ScanType is the type of scan performed by the source
type ScanType int

// String returns the command name of the scan type, e.g. "email".
func (t ScanType) String() string {
	switch t {
	case TypeEmail:
		return "email"
	case TypeUsername:
		return "username"
	case TypeDomain:
		return "domain"
	case TypeKeyword:
		return "keyword"
	case TypePhone:
		return "phone"
	}
	return "unknown"
}

// AllTypes lists every scan type, in CLI order.
var AllTypes = []ScanType{TypeEmail, TypeUsername, TypeDomain, TypeKeyword, TypePhone}

// SupportsScanType reports whether s can run a scan of type t.
func SupportsScanType(s Source, t ScanType) bool {
	return slices.Contains(s.Types(), t)
}

// unsupportedType is the error of a source asked for a type it cannot search.
func unsupportedType(s Source, t ScanType) error {
	return fmt.Errorf("%s cannot run %s searches", s.Name(), t)
}

// newStats returns the stats for a search whose total is not known yet.
func newStats() Stats {
	return Stats{Total: -1}
}

// Types of available scans performed by the source
const (
	TypeEmail ScanType = iota
	TypeUsername
	TypeDomain
	TypeKeyword
	TypePhone
)
