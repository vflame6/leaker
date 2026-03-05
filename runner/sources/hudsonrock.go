package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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
	case TypeDomain:
		url = baseURL + "search-by-domain?domain=" + target
	default:
		// Username, keyword, phone — all fall back to username search
		url = baseURL + "search-by-username?username=" + target
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
		r := s.recordToResult(stealer)
		if r.HasData() {
			results <- r
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
		// Keyword, phone — fall back to username search
		searchType = "username"
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
		r := s.recordToResult(record)
		if r.HasData() {
			results <- r
		}
	}
}

func (s *HudsonRock) recordToResult(record map[string]interface{}) Result {
	r := Result{Source: s.Name()}
	if val, ok := record["email"].(string); ok && val != "" {
		r.Email = val
	}
	if val, ok := record["username"].(string); ok && val != "" {
		r.Username = val
	}
	if val, ok := record["password"].(string); ok && val != "" {
		r.Password = val
	}
	if val, ok := record["ip"].(string); ok && val != "" {
		r.IP = val
	}
	if val, ok := record["url"].(string); ok && val != "" {
		r.URL = val
	}
	if val, ok := record["computer_name"].(string); ok && val != "" {
		r.SetExtra("computer_name", val)
	}
	if val, ok := record["operating_system"].(string); ok && val != "" {
		r.SetExtra("operating_system", val)
	}
	if val, ok := record["date_compromised"].(string); ok && val != "" {
		r.SetExtra("date_compromised", val)
	}
	if val, ok := record["stealer_family"].(string); ok && val != "" {
		r.SetExtra("stealer_family", val)
	}
	return r
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
