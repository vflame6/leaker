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

// Search returns the cached records matching the target. It reads the whole
// lookup so the database query always finishes.
func (s *LocalDB) Search(ctx context.Context, q Query, _ *Session, emit func(Result)) (Stats, error) {
	stats := newStats()
	if s.Lookup == nil {
		return stats, nil
	}
	var firstErr error
	for r := range s.Lookup(ctx, q.Target, q.Type) {
		if r.Error != nil {
			if firstErr == nil {
				firstErr = r.Error
			}
			continue
		}
		emit(r)
	}
	return stats, firstErr
}

// Types: the local database is searched for every type.
func (s *LocalDB) Types() []ScanType { return AllTypes }

// Limits: the database returns every match.
func (s *LocalDB) Limits() Limits { return Limits{} }

func (s *LocalDB) Name() string        { return LocalSourceName }
func (s *LocalDB) UsesKey() bool       { return false }
func (s *LocalDB) NeedsKey() bool      { return false }
func (s *LocalDB) AddApiKeys([]string) {}

// MinInterval is zero: SQLite calls are local and cheap.
func (s *LocalDB) MinInterval() time.Duration { return 0 }

// HasKeys is always false: the local database needs no key.
func (s *LocalDB) HasKeys() bool { return false }
