package sources

import (
	"context"
	"net/http"
	"strings"
)

type Source interface {
	Run(context.Context, string, ScanType, *Session) <-chan Result

	// Name returns the name of the source. It is preferred to use lower case names.
	Name() string

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
	IP       string
	Phone    string
	Name     string
	Database string
	URL      string
	Extra    map[string]string
	Error    error
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
func (r *Result) Value() string {
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
	if r.IP != "" {
		parts = append(parts, "ip:"+r.IP)
	}
	if r.Phone != "" {
		parts = append(parts, "phone:"+r.Phone)
	}
	if r.Name != "" {
		parts = append(parts, "name:"+r.Name)
	}
	if r.Database != "" {
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

// HasData returns true if the result contains at least one data field.
func (r *Result) HasData() bool {
	return r.Email != "" || r.Username != "" || r.Password != "" ||
		r.Hash != "" || r.IP != "" || r.Phone != "" ||
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
