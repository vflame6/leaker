package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/vflame6/leaker/logger"
)

// defaultThreads is the number of targets in flight without -t.
const defaultThreads = 10

// job is one target on its way through the pipeline. Each source's worker
// fills its own run; the collector owns pending.
type job struct {
	index  int
	target string
	runs   []sourceRun

	once  sync.Once
	start time.Time // when the first source started the target

	pending int // sources still searching the target (collector only)
}

// begin logs the target and starts its clock when the first source starts
// it.
func (j *job) begin() {
	j.once.Do(func() {
		j.start = time.Now()
		logger.Infof("Enumerating leaks for %s", j.target)
	})
}

// elapsed is the time since the first source started the target.
func (j *job) elapsed() time.Duration {
	if j.start.IsZero() {
		return 0
	}
	return time.Since(j.start)
}

// event tells the collector that a target was admitted (source < 0) or that
// the source at that index finished it.
type event struct {
	job    *job
	source int
}

// collected is what the collector found while writing the targets.
type collected struct {
	failed int // targets every source failed to search
	errs   []error
}

// EnumerateMultipleTargets searches each line of reader as a target.
//
// Every source has its own worker and queue of targets, so a fast source
// works through the list without waiting for a slow one; the source's
// session handle spaces its requests. At most Threads targets are in flight
// (read but not yet written). A collector gathers each target's results from
// all sources, then filters, deduplicates and writes them, target by target
// in input order, with the target's table.
//
// Lines are read in the background so that an interrupt is noticed at once,
// even while waiting for input. On interrupt no further target starts, the
// results already found are written, "Interrupted" is logged once and the
// context's error is returned.
func (r *Runner) EnumerateMultipleTargets(ctx context.Context, reader io.Reader, writers []io.Writer) error {
	if !r.options.NoFilter {
		logger.Debugf("Results filtering is enabled, leaker will filter results by matching every result to inputted target.")
	} else {
		logger.Debugf("Results filtering is disabled, leaker will not filter any result.")
	}
	if r.session == nil {
		return fmt.Errorf("runner has no HTTP session")
	}

	lines := make(chan string)
	readErr := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				readErr <- nil
				return
			}
		}
		readErr <- scanner.Err()
	}()

	threads := r.options.Threads
	if threads <= 0 {
		threads = defaultThreads
	}
	workers := len(r.scanSources)
	queues := make([]chan *job, workers)
	for i := range queues {
		// At most threads targets are admitted, so a send never blocks.
		queues[i] = make(chan *job, threads)
	}
	events := make(chan event, threads*(workers+1))
	slots := make(chan struct{}, threads)

	var wg sync.WaitGroup
	for i, source := range r.scanSources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queues[i] {
				if err := ctx.Err(); err != nil {
					j.runs[i] = sourceRun{source: source, err: err}
				} else {
					j.begin()
					j.runs[i] = r.searchSource(ctx, source, r.query(source, j.target, r.options.Type))
				}
				events <- event{job: j, source: i}
			}
		}()
	}

	done := make(chan collected, 1)
	go func() { done <- r.collect(ctx, events, workers, slots, writers) }()

	var errs []error
	searched := 0
dispatch:
	for {
		var line string
		var ok bool
		select {
		case <-ctx.Done():
			break dispatch
		case line, ok = <-lines:
		}
		if !ok {
			// The reader also stops when the run is cancelled.
			if ctx.Err() == nil {
				if err := <-readErr; err != nil {
					errs = append(errs, fmt.Errorf("could not read targets: %w", err))
				}
			}
			break
		}
		if ctx.Err() != nil {
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		target := normalizeTarget(r.options.Type, line)
		if !matchesTargetType(r.options.Type, target) {
			logger.Infof("Can't parse input as target, skipping: %s", strings.TrimSpace(line))
			continue
		}

		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		j := &job{index: searched, target: target, runs: make([]sourceRun, workers)}
		searched++
		events <- event{job: j, source: -1}
		for _, queue := range queues {
			queue <- j
		}
	}
	for _, queue := range queues {
		close(queue)
	}
	wg.Wait()
	close(events)
	result := <-done

	if err := ctx.Err(); err != nil {
		logger.Info("Interrupted")
		return err
	}
	errs = append(errs, result.errs...)
	if searched == 0 && len(errs) == 0 {
		return configErrorf("no valid %s target in the input", r.options.Type)
	}
	if result.failed > 0 {
		errs = append(errs, fmt.Errorf("%d of %d targets could not be searched: every source failed", result.failed, searched))
	}
	return errors.Join(errs...)
}

// collect writes the targets in input order as their sources finish them,
// freeing a slot for each written target.
func (r *Runner) collect(ctx context.Context, events <-chan event, workers int, slots <-chan struct{}, writers []io.Writer) collected {
	var result collected
	complete := make(map[int]*job)
	next := 0
	for ev := range events {
		j := ev.job
		if ev.source < 0 {
			j.pending = workers
		} else {
			j.pending--
		}
		if j.pending > 0 {
			continue
		}
		complete[j.index] = j
		for {
			ready, ok := complete[next]
			if !ok {
				break
			}
			delete(complete, next)
			r.writeJob(ctx, ready, writers, &result)
			<-slots
			next++
		}
	}
	return result
}

// writeJob writes one target. After an interrupt only its results are
// written, so nothing already found is lost.
func (r *Runner) writeJob(ctx context.Context, j *job, writers []io.Writer, result *collected) {
	if ctx.Err() != nil {
		r.writeResults(ctx, j.target, r.options.Type, j.runs, writers)
		return
	}
	err := r.finishTarget(ctx, j.target, r.options.Type, j.runs, writers, j.elapsed())
	switch {
	case err == nil:
	case errors.Is(err, errAllSourcesFailed):
		result.failed++ // already reported by finishTarget
	default:
		logger.Errorf("error enumerating %s: %s", j.target, err)
		result.errs = append(result.errs, err)
	}
}
