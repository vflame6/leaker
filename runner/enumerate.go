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

func (r *Runner) EnumerateSingleProbe(probe string, scanType sources.ScanType, timeout time.Duration, writers []io.Writer) error {
	var err error

	logger.Infof("Enumerating leaks for: %s", probe)
	results := make(chan sources.Result)

	wg := &sync.WaitGroup{}
	wg.Add(1)
	// Process the results in a separate goroutine
	go func() {
		for result := range results {
			// check if error
			if result.Error != nil {
				logger.Errorf("error on enumerating line %s: %s", probe, result.Error)
				continue
			}
			// check if filtered
			if !r.options.NoFilter && !strings.Contains(result.Value, probe) {
				continue
			}
			// write result
			for _, writer := range writers {
				err = WritePlainResult(writer, r.options.Verbose, result.Source, result.Value)
				if err != nil {
					logger.Errorf("could not write results for %s: %s", probe, err)
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
				Error:  fmt.Errorf("could not initiate passive session for %s: %s", probe, err),
			}
			return
		}
		defer session.Close()

		// Run each source in parallel on the target probe
		wg := &sync.WaitGroup{}
		for _, s := range ScanSources {
			wg.Add(1)
			go func(s sources.Source) {
				defer wg.Done()
				for result := range s.Run(probe, scanType, session) {
					results <- result
				}
				// sleep to enable source rate-limiting
				// this is done like that because probe enumeration is done one by one
				if !r.options.NoRateLimit {
					time.Sleep(time.Second / time.Duration(s.RateLimit()))
				}
			}(s)
		}
		wg.Wait()
	}()
	wg.Wait()

	logger.Debugf("Finished leaks enumeration for: %s", probe)
	return nil
}
