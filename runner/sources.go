package runner

import "github.com/vflame6/leaker/runner/sources"

// AllSources are used to store all available sources
var AllSources = [...]sources.Source{
	&sources.LeakCheck{},
	&sources.ProxyNova{},
	&sources.OSINTLeak{},
	&sources.IntelX{},
	&sources.BreachDirectory{},
	&sources.LeakLookup{},
	&sources.DeHashed{},
	&sources.Snusbase{},
	&sources.LeakSight{},
}
