package runner

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
)

// maxDBWriteErrors caps how many consecutive local-DB insert failures
// are logged before further errors are silently suppressed for the
// remainder of a single EnumerateSingleTarget call. Prevents flooding
// the terminal when the DB file is on a full or broken disk.
const maxDBWriteErrors = 5

// EnumerateSingleTarget searches one target with every selected source. It
// returns the context's error when the run is interrupted.
func (r *Runner) EnumerateSingleTarget(ctx context.Context, target string, scanType sources.ScanType, writers []io.Writer) error {
	if r.session == nil {
		return fmt.Errorf("runner has no HTTP session")
	}
	session := r.session

	var err error

	logger.Infof("Enumerating leaks for %s", target)
	results := make(chan sources.Result)
	numberOfResults := 0
	timeStart := time.Now()

	wg := &sync.WaitGroup{}

	// Process the results in a separate goroutine
	seen := make(map[string]struct{})
	dbWriteErrors := 0
	dbWriteSuppressed := false
	wg.Add(1)
	go func() {
		defer wg.Done()
		for result := range results {
			// check if error
			if result.Error != nil {
				logger.Errorf("error on enumerating target %s: %s", target, result.Error)
				continue
			}
			// Normalize whitespace on every incoming result before any other
			// check. Sources occasionally return fields that are only spaces
			// (e.g. `name: " "`), which would otherwise slip past HasData()
			// and print as empty `name:` pairs in the output.
			result.TrimSpaces()
			// skip empty results
			if !result.HasData() {
				continue
			}

			// check if filtered
			if !r.options.NoFilter && !result.Matches(scanType, target) {
				continue
			}
			// deduplicate results across sources (unless disabled).
			// Checksum excludes Database so results differing only by source DB are deduped.
			if !r.options.NoDeduplication {
				dedupKey := result.Checksum()
				if _, already := seen[dedupKey]; already {
					continue
				}
				seen[dedupKey] = struct{}{}
			}

			// Persist to local DB BEFORE verifier enrichment so the stored
			// checksum is stable regardless of whether -V was passed, and
			// so results that originated from the local DB itself are not
			// re-written (their Source has been overwritten to "local").
			if r.leakerDB != nil && r.leakerDB.writable && result.Source != sources.LocalSourceName {
				r.dbNotice.Do(func() {
					logger.Infof("Saving results to the local database %s (disable with --no-write-db)", r.leakerDB.path)
				})
				if insertErr := r.leakerDB.Insert(&result); insertErr != nil {
					if !dbWriteSuppressed {
						dbWriteErrors++
						logger.Errorf("could not write result to local DB: %s", insertErr)
						if dbWriteErrors >= maxDBWriteErrors {
							logger.Errorf("local DB writes are failing repeatedly, suppressing further errors")
							dbWriteSuppressed = true
						}
					}
				} else {
					dbWriteErrors = 0
				}
			}

			// enrich result with verification signals if enabled
			r.verifier.EnrichResult(ctx, &result)

			// increase number of results
			numberOfResults++

			// write result
			for _, writer := range writers {
				if r.options.JSON {
					err = WriteJSONResult(writer, &result, target)
				} else {
					err = WritePlainResult(writer, r.options.Verbose, r.options.Metadata, &result)
				}
				if err != nil {
					logger.Errorf("could not write results for %s: %s", target, err)
				}
			}
		}
	}()

	// Partition sources into local (read first, serially) and online
	// (fan out in parallel). Running local first ensures its checksums
	// populate the dedup map before any online result arrives, so a
	// local hit always wins over its online twin.
	var localSources, onlineSources []sources.Source
	for _, s := range r.scanSources {
		if s.Name() == sources.LocalSourceName {
			localSources = append(localSources, s)
		} else {
			onlineSources = append(onlineSources, s)
		}
	}
	// outcomes counts what each source returned, local sources first; each
	// slot is written by one goroutine and read after all have finished.
	outcomes := make([]sourceOutcome, len(localSources)+len(onlineSources))
	forward := func(outcome *sourceOutcome, s sources.Source) bool {
		for result := range s.Run(ctx, target, scanType, session) {
			if result.Error != nil {
				outcome.errors++
			} else {
				outcome.results++
			}
			select {
			case results <- result:
			case <-ctx.Done():
				return false
			}
		}
		return true
	}

	go func() {
		defer close(results)

		// Drain local sources serially. In practice there's at most one,
		// but the loop handles N defensively.
		for i, s := range localSources {
			if !forward(&outcomes[i], s) {
				return
			}
		}

		// If the context was cancelled while draining local sources,
		// skip the online fan-out entirely.
		if ctx.Err() != nil {
			return
		}

		// Fan out online sources in parallel.
		owg := &sync.WaitGroup{}
		for i, s := range onlineSources {
			owg.Add(1)
			go func(outcome *sourceOutcome, s sources.Source) {
				defer owg.Done()

				if !forward(outcome, s) {
					return
				}

				// wait to respect the source's rate limit; targets are
				// searched one by one, so the next target starts after this
				if !r.options.NoRateLimit && s.MinInterval() > 0 {
					select {
					case <-ctx.Done():
					case <-time.After(s.MinInterval()):
					}
				}
			}(&outcomes[len(localSources)+i], s)
		}
		owg.Wait()
	}()
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return err
	}

	timeElapsed := time.Since(timeStart).Truncate(time.Millisecond)
	failed := 0
	for _, o := range outcomes {
		if o.failed() {
			failed++
		}
	}
	if failed > 0 && failed == len(outcomes) {
		// Nothing was searched: never report this as "0 leaks".
		logger.Errorf("Could not search %s: all %d sources failed", target, failed)
		return fmt.Errorf("%w for %s", errAllSourcesFailed, target)
	}
	if failed > 0 {
		logger.Infof("Found %d leaks for %s in %v (%d of %d sources failed)", numberOfResults, target, timeElapsed, failed, len(outcomes))
		return nil
	}
	logger.Infof("Found %d leaks for %s in %v", numberOfResults, target, timeElapsed)
	return nil
}

// sourceOutcome counts what one source returned for one target.
type sourceOutcome struct {
	results, errors int
}

// failed reports whether the source returned errors and nothing else.
func (o sourceOutcome) failed() bool {
	return o.errors > 0 && o.results == 0
}
