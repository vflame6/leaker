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

func (r *Runner) EnumerateSingleTarget(ctx context.Context, target string, scanType sources.ScanType, timeout time.Duration, writers []io.Writer) error {
	var err error

	logger.Infof("Enumerating leaks for %s", target)
	results := make(chan sources.Result)
	numberOfResults := 0
	timeStart := time.Now()

	wg := &sync.WaitGroup{}

	// Process the results in a separate goroutine
	verifier := NewVerifier(r.options.Verify)
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
			if !r.options.NoFilter && !result.Contains(target) {
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
			if r.leakerDB != nil && result.Source != sources.LocalSourceName {
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
			verifier.EnrichResult(&result)

			// increase number of results
			numberOfResults++

			// write result
			for _, writer := range writers {
				if r.options.JSON {
					err = WriteJSONResult(writer, r.options.Metadata, &result, target)
				} else {
					err = WritePlainResult(writer, r.options.Verbose, r.options.Metadata, &result)
				}
				if err != nil {
					logger.Errorf("could not write results for %s: %s", target, err)
				}
			}
		}
	}()

	go func() {
		defer close(results)

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

		session, sessErr := sources.NewSession(timeout, r.options.UserAgent, r.options.Proxy, r.options.Insecure)
		if sessErr != nil {
			results <- sources.Result{
				Error: fmt.Errorf("could not initiate passive session for %s: %w", target, sessErr),
			}
			return
		}
		defer session.Close()

		// Drain local sources serially. In practice there's at most one,
		// but the loop handles N defensively.
		for _, s := range localSources {
			for result := range s.Run(ctx, target, scanType, session) {
				select {
				case results <- result:
				case <-ctx.Done():
					return
				}
			}
		}

		// If the context was cancelled while draining local sources,
		// skip the online fan-out entirely.
		if ctx.Err() != nil {
			return
		}

		// Fan out online sources in parallel.
		owg := &sync.WaitGroup{}
		for _, s := range onlineSources {
			owg.Add(1)
			go func(s sources.Source) {
				defer owg.Done()

				for result := range s.Run(ctx, target, scanType, session) {
					select {
					case results <- result:
					case <-ctx.Done():
						return
					}
				}

				// sleep to enable source rate-limiting
				// this is done like that because target enumeration is done one by one
				if !r.options.NoRateLimit {
					time.Sleep(time.Second / time.Duration(s.RateLimit()))
				}
			}(s)
		}
		owg.Wait()
	}()
	wg.Wait()

	if ctx.Err() != nil {
		logger.Info("Interrupted")
		return nil
	}

	timeElapsed := time.Since(timeStart).Truncate(time.Millisecond)
	logger.Infof("Found %d leaks for %s in %v", numberOfResults, target, timeElapsed)
	return nil
}
