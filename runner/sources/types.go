package sources

import (
	"context"
	"net/http"
)

type Source interface {
	Run(context.Context, string, ScanType, *Session) <-chan Result

	// Name returns the name of the source. It is preferred to use lower case names.
	Name() string

	// IsDefault returns true if the current source should be
	// used as part of the default execution.
	IsDefault() bool

	// NeedsKey returns true if the source requires an API key
	NeedsKey() bool

	AddApiKeys([]string)

	// RateLimit returns how many requests per second can be done to the source
	RateLimit() int
}

type Result struct {
	Source string
	Value  string
	Error  error
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
