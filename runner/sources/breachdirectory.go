package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type BreachDirectory struct {
	apiKeys []string
}

type breachDirectoryResponse struct {
	Found  int                    `json:"found"`
	Result []breachDirectoryEntry `json:"result"`
}

type breachDirectoryEntry struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Sha1     string `json:"sha1"`
	Hash     string `json:"hash"`
	Sources  string `json:"sources"`
}

func (s *BreachDirectory) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name())
		if randomApiKey == "" {
			return
		}

		// BreachDirectory supports auto-detection of input type
		url := fmt.Sprintf("https://breachdirectory.p.rapidapi.com/?func=auto&term=%s", target)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		req.Header.Set("x-rapidapi-key", randomApiKey)
		req.Header.Set("x-rapidapi-host", "breachdirectory.p.rapidapi.com")
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in BreachDirectory source for %s", target)
		resp, err := session.Client.Do(req)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		defer session.DiscardHTTPResponse(resp)

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		logger.Debugf("Response from BreachDirectory source: status code [%d], size [%d]", resp.StatusCode, len(body))

		if resp.StatusCode != http.StatusOK {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("BreachDirectory returned status %d: %s", resp.StatusCode, string(body)),
			}
			return
		}

		var response breachDirectoryResponse
		if err := json.Unmarshal(body, &response); err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		for _, entry := range response.Result {
			var parts []string
			if entry.Email != "" {
				parts = append(parts, "email:"+entry.Email)
			}
			if entry.Password != "" {
				parts = append(parts, "password:"+entry.Password)
			}
			if entry.Sha1 != "" {
				parts = append(parts, "sha1:"+entry.Sha1)
			}
			if entry.Hash != "" {
				parts = append(parts, "hash:"+entry.Hash)
			}
			if entry.Sources != "" {
				parts = append(parts, "sources:"+entry.Sources)
			}

			if len(parts) > 0 {
				results <- Result{
					Source: s.Name(),
					Value:  strings.Join(parts, ", "),
				}
			}
		}
	}()

	return results
}

func (s *BreachDirectory) Name() string {
	return "breachdirectory"
}

func (s *BreachDirectory) IsDefault() bool {
	return false
}

func (s *BreachDirectory) NeedsKey() bool {
	return true
}

func (s *BreachDirectory) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

func (s *BreachDirectory) RateLimit() int {
	// Free tier: 10 requests/month; paid tiers higher
	return 1
}
