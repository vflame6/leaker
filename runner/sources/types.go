package sources

import "net/http"

type Source interface {
	Run(string, *Session) <-chan Result

	// Name returns the name of the source. It is preferred to use lower case names.
	Name() string

	// IsDefault returns true if the current source should be
	// used as part of the default execution.
	IsDefault() bool

	// NeedsKey returns true if the source requires an API key
	NeedsKey() bool

	AddApiKeys([]string)
}

type Result struct {
	Source string
	Value  string
	Error  error
}

type Session struct {
	Client *http.Client
}
