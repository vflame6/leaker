package runner

import (
	"fmt"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
	"io"
	"strings"
	"sync"
	"time"
)

func (r *Runner) EnumerateSingleTarget(target string, scanType sources.ScanType, timeout time.Duration, writers []io.Writer) error {
	var err error

	logger.Infof("Enumerating leaks for %s", target)
	results := make(chan sources.Result)
	numberOfResults := 0
	timeStart := time.Now()

	wg := &sync.WaitGroup{}

	// Process the results in a separate goroutine
	wg.Add(1)
	go func() {
		for result := range results {
			// check if error
			if result.Error != nil {
				logger.Errorf("error on enumerating target %s: %s", target, result.Error)
				continue
			}
			// check if filtered
			if !r.options.NoFilter && !strings.Contains(result.Value, target) {
				continue
			}

			// increase number of results
			numberOfResults++

			// write result
			for _, writer := range writers {
				err = WritePlainResult(writer, r.options.Verbose, result.Source, result.Value)
				if err != nil {
					logger.Errorf("could not write results for %s: %s", target, err)
				}
			}
		}
		wg.Done()
	}()

	go func() {
		defer close(results)

		session, err := sources.NewSession(timeout, r.options.UserAgent, r.options.Proxy)
		if err != nil {
			results <- sources.Result{
				Source: "",
				Value:  "",
				Error:  fmt.Errorf("could not initiate passive session for %s: %s", target, err),
			}
			return
		}
		defer session.Close()

		// Run each source in parallel on the target
		wg := &sync.WaitGroup{}
		for _, s := range ScanSources {
			wg.Add(1)
			go func(s sources.Source) {
				defer wg.Done()
				for result := range s.Run(target, scanType, session) {
					results <- result
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

	timeElapsed := time.Since(timeStart).Truncate(time.Millisecond)
	logger.Infof("Found %d leaks for %s in %v", numberOfResults, target, timeElapsed)
	return nil
}
