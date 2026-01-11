package runner

import (
	"fmt"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
	"io"
	"sync"
	"time"
)

func (r *Runner) EnumerateSingleEmail(email string, timeout time.Duration, writers []io.Writer) error {
	var err error

	logger.Infof("Enumerating leaks for email %s", email)
	results := make(chan sources.Result)

	wg := &sync.WaitGroup{}
	wg.Add(1)
	// Process the results in a separate goroutine
	go func() {
		for result := range results {
			if result.Error != nil {
				logger.Errorf("error enumerating email %s: %s", email, result.Error)
				continue
			}
			for _, writer := range writers {
				err = WritePlainResult(writer, r.options.Verbose, result.Source, result.Value)
				if err != nil {
					logger.Errorf("could not write results for %s: %s", email, err)
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
				Error:  fmt.Errorf("could not initiate passive session for %s: %s", email, err),
			}
			return
		}
		defer session.Close()

		// Run each source in parallel on the target email
		wg := &sync.WaitGroup{}
		for _, s := range ScanSources {
			wg.Add(1)
			go func(s sources.Source) {
				defer wg.Done()
				for result := range s.Run(email, session) {
					results <- result
				}
				// sleep to enable source rate-limiting
				// this is done like that because email enumeration is done one by one
				if !r.options.NoRateLimit {
					time.Sleep(time.Second / time.Duration(s.RateLimit()))
				}
			}(s)
		}
		wg.Wait()
	}()
	wg.Wait()

	logger.Debugf("Finished leaks enumeration for email %s", email)
	return nil
}
