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

type HudsonRock struct {
	apiKeys []string
}

type hudsonRockFreeResponse struct {
	Stealers []map[string]interface{} `json:"stealers"`
}

type hudsonRockPaidResponse struct {
	Data []map[string]interface{} `json:"data"`
}

func (s *HudsonRock) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name())

		if randomApiKey == "" {
			// Use free OSINT endpoints
			s.runFree(ctx, target, scanType, session, results)
		} else {
			// Use paid Cavalier v3 API
			s.runPaid(ctx, target, scanType, session, results, randomApiKey)
		}
	}()

	return results
}

func (s *HudsonRock) runFree(ctx context.Context, target string, scanType ScanType, session *Session, results chan<- Result) {
	const baseURL = "https://cavalier.hudsonrock.com/api/json/v2/osint-tools/"

	var url string
	switch scanType {
	case TypeEmail:
		url = baseURL + "search-by-email?email=" + target
	case TypeUsername:
		url = baseURL + "search-by-username?username=" + target
	case TypeDomain:
		url = baseURL + "search-by-domain?domain=" + target
	default:
		results <- Result{
			Source: s.Name(),
			Error:  fmt.Errorf("HudsonRock free API does not support scan type %d", scanType),
		}
		return
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		results <- Result{Source: s.Name(), Error: err}
		return
	}
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in HudsonRock (free) source for %s", target)
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
	logger.Debugf("Response from HudsonRock (free) source: status code [%d], size [%d]", resp.StatusCode, len(body))

	if resp.StatusCode != http.StatusOK {
		results <- Result{
			Source: s.Name(),
			Error:  fmt.Errorf("HudsonRock returned status %d: %s", resp.StatusCode, string(body)),
		}
		return
	}

	var response hudsonRockFreeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		results <- Result{Source: s.Name(), Error: err}
		return
	}

	for _, stealer := range response.Stealers {
		parts := s.extractFields(stealer)
		if len(parts) > 0 {
			results <- Result{
				Source: s.Name(),
				Value:  strings.Join(parts, ", "),
			}
		}
	}
}

func (s *HudsonRock) runPaid(ctx context.Context, target string, scanType ScanType, session *Session, results chan<- Result, apiKey string) {
	const baseURL = "https://api.hudsonrock.com/json/v3/search-by-domain"

	var searchType string
	switch scanType {
	case TypeEmail:
		searchType = "email"
	case TypeUsername:
		searchType = "username"
	case TypeDomain:
		searchType = "domain"
	default:
		results <- Result{
			Source: s.Name(),
			Error:  fmt.Errorf("HudsonRock paid API does not support scan type %d", scanType),
		}
		return
	}

	url := fmt.Sprintf("%s?type=%s&query=%s", baseURL, searchType, target)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		results <- Result{Source: s.Name(), Error: err}
		return
	}
	req.Header.Set("api-key", apiKey)
	req.Header.Set("Accept", "application/json")

	logger.Debugf("Sending a request in HudsonRock (paid) source for %s", target)
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
	logger.Debugf("Response from HudsonRock (paid) source: status code [%d], size [%d]", resp.StatusCode, len(body))

	if resp.StatusCode != http.StatusOK {
		results <- Result{
			Source: s.Name(),
			Error:  fmt.Errorf("HudsonRock paid API returned status %d: %s", resp.StatusCode, string(body)),
		}
		return
	}

	var response hudsonRockPaidResponse
	if err := json.Unmarshal(body, &response); err != nil {
		results <- Result{Source: s.Name(), Error: err}
		return
	}

	for _, record := range response.Data {
		parts := s.extractFields(record)
		if len(parts) > 0 {
			results <- Result{
				Source: s.Name(),
				Value:  strings.Join(parts, ", "),
			}
		}
	}
}

func (s *HudsonRock) extractFields(record map[string]interface{}) []string {
	var parts []string
	for _, field := range []string{"url", "username", "email", "password", "computer_name", "operating_system", "ip", "date_compromised", "stealer_family"} {
		if val, ok := record[field].(string); ok && val != "" {
			parts = append(parts, field+":"+val)
		}
	}
	return parts
}

func (s *HudsonRock) Name() string {
	return "hudsonrock"
}


func (s *HudsonRock) NeedsKey() bool {
	return false
}

func (s *HudsonRock) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

func (s *HudsonRock) RateLimit() int {
	return 10
}
