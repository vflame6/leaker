package runner

import "github.com/vflame6/leaker/runner/sources"

type Source interface {
	Run(email string) <-chan sources.Result

	// Name returns the name of the source. It is preferred to use lower case names.
	Name() string

	// IsDefault returns true if the current source should be
	// used as part of the default execution.
	IsDefault() bool

	// NeedsKey returns true if the source requires an API key
	NeedsKey() bool

	AddApiKeys([]string)

	GetRateLimit() int
}

var AllSources = [...]Source{
	&sources.ProxyNova{},
}
