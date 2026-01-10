package runner

import "github.com/vflame6/leaker/runner/sources"

// AllSources are used to store all available sources
var AllSources = [...]sources.Source{
	&sources.LeakCheck{},
	&sources.ProxyNova{},
}

// ScanSources variable is used to store sources that will be used in actual scan
var ScanSources []sources.Source
