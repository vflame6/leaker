package runner

import (
	"fmt"
	"github.com/vflame6/leaker/runner/sources"
	"log"
	"sync"
)

func (r *Runner) EnumerateSingleEmail(email string) error {
	log.Printf("enumerating email %s", email)

	results := make(chan sources.Result)

	//now := time.Now()

	// Run each source in parallel on the target domain
	wg := &sync.WaitGroup{}
	// parse results
	go func() {
		for result := range results {
			fmt.Printf("[%s] %s\n", result.Source, result.Value)
		}
	}()
	// run enumeration
	for _, s := range AllSources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			for result := range s.Run(email) {
				results <- result
			}
		}(s)
	}
	wg.Wait()
	close(results)

	return nil
}
