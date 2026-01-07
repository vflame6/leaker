package runner

import "github.com/vflame6/leaker/runner/sources"

var AllSources = [...]sources.Source{
	&sources.LeakCheck{},
	&sources.ProxyNova{},
}
