package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
)

type Source interface {
	Run(context.Context, string, ScanType, *Session) <-chan Result

	// Name returns the name of the source. It is preferred to use lower case names.
	Name() string

	// UsesKey returns true if the source supports an API key
	UsesKey() bool

	// NeedsKey returns true if the source requires an API key
	NeedsKey() bool

	AddApiKeys([]string)

	// RateLimit returns how many requests per second can be done to the source
	RateLimit() int
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
	for k, v := range r.Extra {
		parts = append(parts, k+":"+v)
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

// Contains returns true if any field in the result contains the given substring (case-insensitive).
func (r *Result) Contains(target string) bool {
	t := strings.ToLower(target)
	return strings.Contains(strings.ToLower(r.Email), t) ||
		strings.Contains(strings.ToLower(r.Username), t) ||
		strings.Contains(strings.ToLower(r.Password), t) ||
		strings.Contains(strings.ToLower(r.Hash), t) ||
		strings.Contains(strings.ToLower(r.Salt), t) ||
		strings.Contains(strings.ToLower(r.IP), t) ||
		strings.Contains(strings.ToLower(r.Phone), t) ||
		strings.Contains(strings.ToLower(r.Name), t) ||
		strings.Contains(strings.ToLower(r.Database), t) ||
		strings.Contains(strings.ToLower(r.URL), t)
}

// CustomTransport wraps http.Transport and adds a default User-Agent header.
type CustomTransport struct {
	Transport http.RoundTripper
	UserAgent string
}

type Session struct {
	Client *http.Client
}

// ScanType is the type of scan performed by the source
type ScanType int

// Types of available scans performed by the source
const (
	TypeEmail ScanType = iota
	TypeUsername
	TypeDomain
	TypeKeyword
	TypePhone
)
