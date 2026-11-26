package sources

import (
	"context"
	"time"
)

// LocalSourceName is the name used by the LocalDB source. It is also the
// token users pass to `-s local` on the CLI.
const LocalSourceName = "local"

// LocalDBLookup is the function the runner injects into LocalDB at startup
// to perform the actual database query. It lives as a func type to avoid
// an import cycle: the runner package imports sources, so sources cannot
// directly reference runner.LeakerDB.
type LocalDBLookup func(ctx context.Context, target string, scanType ScanType) <-chan Result

// LocalDB is a Source that reads from the local SQLite cache instead of
// hitting an online API. It is added to AllSources so --list-sources
// discovers it, and is pulled out of the parallel fan-out by the runner
// so local results arrive first and populate the dedup map before any
// online source produces a result.
type LocalDB struct {
	Lookup LocalDBLookup
}

func (s *LocalDB) Run(ctx context.Context, target string, scanType ScanType, _ *Session) <-chan Result {
	if s.Lookup == nil {
		out := make(chan Result)
		close(out)
		return out
	}
	return s.Lookup(ctx, target, scanType)
}

func (s *LocalDB) Name() string        { return LocalSourceName }
func (s *LocalDB) UsesKey() bool       { return false }
func (s *LocalDB) NeedsKey() bool      { return false }
func (s *LocalDB) AddApiKeys([]string) {}

// MinInterval is zero: SQLite calls are local and cheap.
func (s *LocalDB) MinInterval() time.Duration { return 0 }

// HasKeys is always false: the local database needs no key.
func (s *LocalDB) HasKeys() bool { return false }
