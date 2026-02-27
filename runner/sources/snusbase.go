package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type Snusbase struct {
	apiKeys []string
}

type snusbaseSearchRequest struct {
	Terms   []string `json:"terms"`
	Types   []string `json:"types"`
	GroupBy string   `json:"group_by,omitempty"`
}

type snusbaseSearchResponse struct {
	Took    float64                             `json:"took"`
	Size    int                                 `json:"size"`
	Results map[string][]map[string]interface{} `json:"results"`
}

func (s *Snusbase) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name())
		if randomApiKey == "" {
			return
		}

		var searchTypes []string
		switch scanType {
		case TypeEmail:
			searchTypes = []string{"email"}
		case TypeUsername:
			searchTypes = []string{"username"}
		case TypeDomain:
			searchTypes = []string{"_domain"}
		case TypeKeyword:
			searchTypes = []string{"password"}
		case TypePhone:
			searchTypes = []string{"email", "username"}
		}

		searchReq := snusbaseSearchRequest{
			Terms: []string{target},
			Types: searchTypes,
		}

		body, err := json.Marshal(searchReq)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.snusbase.com/data/search",
			bytes.NewReader(body))
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		req.Header.Set("Auth", randomApiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in Snusbase source for %s", target)
		resp, err := session.Client.Do(req)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		defer session.DiscardHTTPResponse(resp)

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		logger.Debugf("Response from Snusbase source: status code [%d], size [%d]", resp.StatusCode, len(respBody))

		if resp.StatusCode != http.StatusOK {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("Snusbase returned status %d: %s", resp.StatusCode, string(respBody)),
			}
			return
		}

		var response snusbaseSearchResponse
		if err := json.Unmarshal(respBody, &response); err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		for dbName, records := range response.Results {
			for _, record := range records {
				var parts []string
				parts = append(parts, "database:"+dbName)
				for _, field := range []string{"email", "username", "password", "hash", "lastip", "name", "salt"} {
					if val, ok := record[field].(string); ok && val != "" {
						parts = append(parts, field+":"+val)
					}
				}
				if len(parts) > 1 { // more than just database name
					results <- Result{
						Source: s.Name(),
						Value:  strings.Join(parts, ", "),
					}
				}
			}
		}
	}()

	return results
}

func (s *Snusbase) Name() string {
	return "snusbase"
}

func (s *Snusbase) IsDefault() bool {
	return false
}

func (s *Snusbase) NeedsKey() bool {
	return true
}

func (s *Snusbase) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

func (s *Snusbase) RateLimit() int {
	return 2
}
