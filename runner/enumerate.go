package runner

import (
	"fmt"
	"github.com/vflame6/leaker/runner/sources"
	"log"
	"sync"
	"time"
)

func (r *Runner) EnumerateSingleEmail(email string, timeout time.Duration) error {
	log.Printf("enumerating email %s", email)
	results := make(chan sources.Result)

	wg := &sync.WaitGroup{}
	wg.Add(1)
	// Process the results in a separate goroutine
	go func() {
		for result := range results {
			fmt.Printf("[%s] %s\n", result.Source, result.Value)
		}
		wg.Done()
	}()

	go func() {
		defer close(results)

		session, err := sources.NewSession(timeout)
		if err != nil {
			results <- sources.Result{
				Source: "",
				Value:  "",
				Error:  fmt.Errorf("could not init passive session for %s: %s", email, err),
			}
			return
		}
		defer session.Close()

		// Run each source in parallel on the target email
		wg := &sync.WaitGroup{}
		for _, s := range AllSources {
			wg.Add(1)
			go func(s sources.Source) {
				defer wg.Done()
				for result := range s.Run(email, session) {
					if result.Error != nil {
						log.Printf("[%s] %v\n", result.Source, result.Error)
						continue
					}
					results <- result
				}
			}(s)
		}
		wg.Wait()
	}()
	wg.Wait()

	return nil
}
