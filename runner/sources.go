package runner

import "github.com/vflame6/leaker/runner/sources"

// AllSources are used to store all available sources.
// LocalDB is included so --list-sources discovers it, but it is excluded
// from the default `-s online` resolution in configureSources.
var AllSources = [...]sources.Source{
	&sources.BreachDirectory{},
	&sources.DeHashed{},
	&sources.HudsonRock{},
	&sources.IntelX{},
	&sources.LeakCheck{},
	&sources.LeakLookup{},
	&sources.LeakSight{},
	&sources.LocalDB{},
	&sources.OSINTLeak{},
	&sources.ProxyNova{},
	&sources.Snusbase{},
	&sources.WeLeakInfo{},
	&sources.WhiteIntel{},
}
