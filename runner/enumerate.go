package runner

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
)

// maxDBWriteErrors caps how many consecutive local-DB insert failures
// are logged before further errors are silently suppressed for the
// remainder of one target. Prevents flooding the terminal when the DB file
// is on a full or broken disk.
const maxDBWriteErrors = 5

// EnumerateSingleTarget searches one target with every selected source at
// once, then writes what they found and the target's table. It returns the
// context's error when the run is interrupted; results found before the
// interrupt are still written.
func (r *Runner) EnumerateSingleTarget(ctx context.Context, target string, scanType sources.ScanType, writers []io.Writer) error {
	if r.session == nil {
		return fmt.Errorf("runner has no HTTP session")
	}
	logger.Infof("Enumerating leaks for %s", target)
	start := time.Now()

	runs := make([]sourceRun, len(r.scanSources))
	var wg sync.WaitGroup
	for i, s := range r.scanSources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs[i] = r.searchSource(ctx, s, r.query(s, target, scanType))
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		r.writeResults(ctx, target, scanType, runs, writers)
		return err
	}
	return r.finishTarget(ctx, target, scanType, runs, writers, time.Since(start))
}

// query is the search of one target by source s, with the run's limits. A
// source's own default request budget (IntelX's file reads) replaces the
// run's default when --max-requests was not given; --all (0) stays.
func (r *Runner) query(s sources.Source, target string, scanType sources.ScanType) sources.Query {
	q := sources.Query{Target: target, Type: scanType, MaxResults: r.options.MaxResults, MaxRequests: r.options.MaxRequests}
	if r.sourceDefault(s) {
		q.MaxRequests = s.Limits().DefaultRequests
	}
	return q
}

// sourceDefault reports whether source s searches with its own default
// request budget.
func (r *Runner) sourceDefault(s sources.Source) bool {
	return s.Limits().DefaultRequests > 0 && r.options.MaxRequests > 0 && !r.options.MaxRequestsTyped
}

// searchSource runs one search of source s. The runner, not the source,
// counts what was emitted and the requests sent, and stops taking results
// at --max-results, so sources that answer with everything at once are cut
// there too.
func (r *Runner) searchSource(ctx context.Context, s sources.Source, q sources.Query) sourceRun {
	session := r.sessionFor(s)
	before := session.Requests()
	run := sourceRun{source: s}
	var mu sync.Mutex
	dropped := false
	start := time.Now()
	stats, err := s.Search(ctx, q, session, func(result sources.Result) {
		mu.Lock()
		defer mu.Unlock()
		if q.MaxResults > 0 && len(run.results) >= q.MaxResults {
			dropped = true
			return
		}
		run.results = append(run.results, result)
	})
	run.elapsed = time.Since(start)
	mu.Lock()
	defer mu.Unlock()
	stats.Requests = session.Requests() - before
	stats.Fetched = len(run.results)
	if dropped {
		stats.Truncated, stats.Limit = true, sources.LimitResults
	}
	run.stats, run.err = stats, err
	return run
}

// finishTarget writes the results of a searched target, then its table
// (with --stats) and summary line, and records the table for --stats-file. A target that every
// source failed to search is errAllSourcesFailed, never "0 leaks".
func (r *Runner) finishTarget(ctx context.Context, target string, scanType sources.ScanType, runs []sourceRun, writers []io.Writer, elapsed time.Duration) error {
	kept := r.writeResults(ctx, target, scanType, runs, writers)

	summary := TargetSummary{Target: target, TimeMS: elapsed.Milliseconds()}
	failed := 0
	for i := range runs {
		run := &runs[i]
		if run.err != nil {
			logger.Errorf("%s failed for %s: %s", run.source.Name(), target, run.err)
		}
		if run.failed() {
			failed++
		}
		summary.Found += kept[i]
	}
	summary.Sources = r.summaryRows(runs, kept)
	r.recordTarget(summary)
	if r.options.Summary && !r.options.Quiet {
		if err := writeSummaryTable(r.summaryOutput(), summary); err != nil {
			logger.Errorf("could not print the summary of %s: %s", target, err)
		}
	}
	for i := range runs {
		if run := &runs[i]; run.stats.Truncated && run.err == nil {
			logger.Warnf("%s", truncationNotice(run.source.Name(), run.stats, run.source.Limits(), r.sourceDefault(run.source)))
		}
	}

	took := elapsed.Truncate(time.Millisecond)
	switch {
	case failed > 0 && failed == len(runs):
		logger.Errorf("Could not search %s: all %d sources failed", target, failed)
		return fmt.Errorf("%w for %s", errAllSourcesFailed, target)
	case failed > 0:
		logger.Infof("Found %d leaks for %s in %v (%d of %d sources failed)", summary.Found, target, took, failed, len(runs))
	default:
		logger.Infof("Found %d leaks for %s in %v", summary.Found, target, took)
	}
	return nil
}

// writeResults filters, deduplicates, stores and writes the results of one
// target and returns how many of each run's results were kept. Local
// results go first, so a record found both locally and online is kept as
// the local one.
func (r *Runner) writeResults(ctx context.Context, target string, scanType sources.ScanType, runs []sourceRun, writers []io.Writer) []int {
	kept := make([]int, len(runs))
	order := make([]int, 0, len(runs))
	for i := range runs {
		order = append(order, i)
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return boolOrder(runs[b].source.Name() == sources.LocalSourceName, runs[a].source.Name() == sources.LocalSourceName)
	})

	seen := make(map[string]struct{})
	dbWriteErrors := 0
	dbWriteSuppressed := false
	for _, i := range order {
		run := &runs[i]
		for _, result := range run.results {
			if result.Source == "" {
				result.Source = run.source.Name()
			}
			// Sources occasionally return fields that are only spaces
			// (e.g. `name: " "`), which would otherwise slip past HasData()
			// and print as empty `name:` pairs in the output.
			result.TrimSpaces()
			if !result.HasData() {
				continue
			}
			if !r.options.NoFilter && !result.Matches(scanType, target) {
				continue
			}
			// Checksum excludes Database so results differing only by
			// source DB are deduplicated.
			if !r.options.NoDeduplication {
				key := result.Checksum()
				if _, already := seen[key]; already {
					continue
				}
				seen[key] = struct{}{}
			}

			// Never write back what came from the local DB itself.
			if r.leakerDB != nil && r.leakerDB.writable && result.Source != sources.LocalSourceName {
				r.dbNotice.Do(func() {
					logger.Infof("Saving results to the local database %s", r.leakerDB.path)
				})
				if err := r.leakerDB.Insert(&result); err != nil {
					if !dbWriteSuppressed {
						dbWriteErrors++
						logger.Errorf("could not write result to local DB: %s", err)
						if dbWriteErrors >= maxDBWriteErrors {
							logger.Errorf("local DB writes are failing repeatedly, suppressing further errors")
							dbWriteSuppressed = true
						}
					}
				} else {
					dbWriteErrors = 0
				}
			}

			kept[i]++

			for _, writer := range writers {
				var err error
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
	}
	return kept
}

// boolOrder orders false before true.
func boolOrder(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// summaryRows returns the rows of a target's table: one per selected source,
// in source order, skipped sources included.
func (r *Runner) summaryRows(runs []sourceRun, kept []int) []SourceSummary {
	byName := make(map[string]SourceSummary, len(runs))
	for i := range runs {
		byName[runs[i].source.Name()] = runs[i].summary(kept[i])
	}
	rows := make([]SourceSummary, 0, len(r.selected)+len(runs))
	listed := make(map[string]bool)
	for _, name := range r.selected {
		if row, ok := byName[name]; ok {
			rows = append(rows, row)
			listed[name] = true
		} else if status, ok := r.skipped[name]; ok {
			rows = append(rows, SourceSummary{Source: name, Status: status})
			listed[name] = true
		}
	}
	for i := range runs {
		if name := runs[i].source.Name(); !listed[name] {
			rows = append(rows, byName[name])
		}
	}
	return rows
}

// recordTarget keeps a target's table for --stats-file.
func (r *Runner) recordTarget(summary TargetSummary) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	if r.run != nil {
		r.run.Targets = append(r.run.Targets, summary)
	}
}

// summaryOutput is where the per-target tables go.
func (r *Runner) summaryOutput() io.Writer {
	if r.options.SummaryOutput != nil {
		return r.options.SummaryOutput
	}
	return defaultSummaryOutput
}

// sessionFor returns the session handle of source s for this run. It waits
// MinInterval between two requests of the source (not with --no-rate-limit),
// pages and retries included, across all targets.
func (r *Runner) sessionFor(s sources.Source) *sources.Session {
	r.sessionsMu.Lock()
	defer r.sessionsMu.Unlock()
	if r.sourceSessions == nil {
		r.sourceSessions = make(map[string]*sources.Session)
	}
	handle, ok := r.sourceSessions[s.Name()]
	if !ok {
		interval := s.MinInterval()
		if r.options.NoRateLimit {
			interval = 0
		}
		handle = r.session.ForSource(interval)
		r.sourceSessions[s.Name()] = handle
	}
	return handle
}
