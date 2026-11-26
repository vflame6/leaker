package runner

import "github.com/vflame6/leaker/runner/sources"

// NewSources returns a fresh instance of every source. Each runner loads its
// API keys into its own set, so keys and the local DB handle never leak
// between runs. LocalDB is included; the "online" group leaves it out.
func NewSources() []sources.Source {
	return []sources.Source{
		&sources.BreachDirectory{},
		&sources.BreachHouse{},
		&sources.DeHashed{},
		&sources.HIBR{},
		&sources.HudsonRock{},
		&sources.IntelX{},
		&sources.LeakCheck{},
		&sources.LeakRadar{},
		&sources.LeakLookup{},
		&sources.LeakSight{},
		&sources.LocalDB{},
		&sources.OSINTLeak{},
		&sources.ProxyNova{},
		&sources.Snusbase{},
		&sources.WeLeakInfo{},
		&sources.WhiteIntel{},
	}
}

// AllSources lists every source for --list-sources and the provider config
// template. It never holds keys; runs use NewSources.
var AllSources = NewSources()
