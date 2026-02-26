package runner

import (
	"context"
	"fmt"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
	"io"
	"strings"
	"sync"
	"time"
)

func (r *Runner) EnumerateSingleTarget(ctx context.Context, target string, scanType sources.ScanType, timeout time.Duration, writers []io.Writer) error {
	var err error

	logger.Infof("Enumerating leaks for %s", target)
	results := make(chan sources.Result)
	numberOfResults := 0
	timeStart := time.Now()

	wg := &sync.WaitGroup{}

	// Process the results in a separate goroutine
	seen := make(map[string]struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for result := range results {
			// check if error
			if result.Error != nil {
				logger.Errorf("error on enumerating target %s: %s", target, result.Error)
				continue
			}
			// check if filtered
			if !r.options.NoFilter && !strings.Contains(strings.ToLower(result.Value), target) {
				continue
			}
			// deduplicate results across sources (unless disabled)
			if !r.options.ShowDuplicates {
				if _, already := seen[result.Value]; already {
					continue
				}
				seen[result.Value] = struct{}{}
			}

			// increase number of results
			numberOfResults++

			// write result
			for _, writer := range writers {
				if r.options.JSON {
					err = WriteJSONResult(writer, result.Source, result.Value, target)
				} else {
					err = WritePlainResult(writer, r.options.Verbose, result.Source, result.Value)
				}
				if err != nil {
					logger.Errorf("could not write results for %s: %s", target, err)
				}
			}
		}
	}()

	go func() {
		defer close(results)

		session, err := sources.NewSession(timeout, r.options.UserAgent, r.options.Proxy, r.options.Insecure)
		if err != nil {
			results <- sources.Result{
				Error: fmt.Errorf("could not initiate passive session for %s: %w", target, err),
			}
			return
		}
		defer session.Close()

		// Run each source in parallel on the target
		wg := &sync.WaitGroup{}
		for _, s := range r.scanSources {
			wg.Add(1)
			go func(s sources.Source) {
				defer wg.Done()
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
		wg.Wait()
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
