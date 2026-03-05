package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

type WhiteIntel struct {
	apiKeys []string
}

type whiteIntelRequest struct {
	APIKey   string `json:"apikey"`
	Query    string `json:"query"`
	Type     string `json:"type"`
	Limit    int    `json:"limit"`
	Page     int    `json:"page"`
	Username string `json:"username,omitempty"`
}

type whiteIntelResult struct {
	DataType    string `json:"data_type"`
	URL         string `json:"url"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	LogDate     string `json:"log_date"`
	Hostname    string `json:"hostname"`
	IP          string `json:"ip"`
	MalwarePath string `json:"malware_path"`
}

type whiteIntelResponse struct {
	Success bool               `json:"success"`
	Results []whiteIntelResult `json:"results"`
}

func (s *WhiteIntel) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
		if randomApiKey == "" {
			return
		}

		searchReq := whiteIntelRequest{
			APIKey: randomApiKey,
			Query:  target,
			Type:   "all",
			Limit:  500,
			Page:   1,
		}

		switch scanType {
		case TypeEmail:
			searchReq.Query = target
		case TypeDomain:
			searchReq.Query = target
		case TypeUsername:
			searchReq.Query = target
			searchReq.Username = target
		default:
			// Keyword, phone — fall back to username search
			searchReq.Query = target
			searchReq.Username = target
		}

		body, err := json.Marshal(searchReq)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.whiteintel.io/get_consumer_leaks.php",
			bytes.NewReader(body))
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in WhiteIntel source for %s", target)
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
		logger.Debugf("Response from WhiteIntel source: status code [%d], size [%d]", resp.StatusCode, len(respBody))

		if resp.StatusCode != http.StatusOK {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("WhiteIntel returned status %d: %s", resp.StatusCode, string(respBody)),
			}
			return
		}

		var response whiteIntelResponse
		if err := json.Unmarshal(respBody, &response); err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		if !response.Success {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("WhiteIntel request failed: %s", string(respBody)),
			}
			return
		}

		for _, record := range response.Results {
			r := Result{
				Source:   s.Name(),
				Username: record.Username,
				Password: record.Password,
				IP:       record.IP,
				URL:      record.URL,
			}
			if record.DataType != "" {
				r.SetExtra("data_type", record.DataType)
			}
			if record.Hostname != "" {
				r.SetExtra("hostname", record.Hostname)
			}
			if record.LogDate != "" {
				r.SetExtra("log_date", record.LogDate)
			}
			if record.MalwarePath != "" {
				r.SetExtra("malware_path", record.MalwarePath)
			}
			if r.HasData() {
				results <- r
			}
		}
	}()

	return results
}

func (s *WhiteIntel) Name() string {
	return "whiteintel"
}

func (s *WhiteIntel) UsesKey() bool {
	return true
}

func (s *WhiteIntel) NeedsKey() bool {
	return true
}

func (s *WhiteIntel) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

func (s *WhiteIntel) RateLimit() int {
	return 5
}
