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

type WeLeakInfo struct {
	apiKeys []string
}

type weLeakInfoRequest struct {
	Query    string `json:"query"`
	Type     string `json:"type"`
	Limit    string `json:"limit"`
	Wildcard string `json:"wildcard"`
}

type weLeakInfoResponse struct {
	Data []map[string]interface{} `json:"Data"`
}

func (s *WeLeakInfo) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name())
		if randomApiKey == "" {
			return
		}

		// Extract private key from "pub_key:priv_key" format
		bearerToken := randomApiKey
		if idx := strings.LastIndex(randomApiKey, ":"); idx != -1 {
			bearerToken = randomApiKey[idx+1:]
		}

		searchReq := weLeakInfoRequest{
			Query:    target,
			Limit:    "1000",
			Wildcard: "false",
		}

		switch scanType {
		case TypeEmail:
			searchReq.Type = "email"
		case TypeUsername:
			searchReq.Type = "username"
		case TypeDomain:
			searchReq.Query = "*@" + target
			searchReq.Type = "email"
			searchReq.Wildcard = "true"
		case TypeKeyword:
			searchReq.Type = "password"
		default:
			// Phone and any other types — fall back to username search
			searchReq.Type = "username"
		}

		body, err := json.Marshal(searchReq)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.weleakinfo.io/v3/search",
			bytes.NewReader(body))
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		req.Header.Set("Authorization", "Bearer "+bearerToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in WeLeakInfo source for %s", target)
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
		logger.Debugf("Response from WeLeakInfo source: status code [%d], size [%d]", resp.StatusCode, len(respBody))

		if resp.StatusCode != http.StatusOK {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("WeLeakInfo returned status %d: %s", resp.StatusCode, string(respBody)),
			}
			return
		}

		var response weLeakInfoResponse
		if err := json.Unmarshal(respBody, &response); err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		for _, record := range response.Data {
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
			if val, ok := record["hash"].(string); ok && val != "" {
				r.Hash = val
			}
			if val, ok := record["name"].(string); ok && val != "" {
				r.Name = val
			}
			if val, ok := record["ip"].(string); ok && val != "" {
				r.IP = val
			}
			if val, ok := record["phone"].(string); ok && val != "" {
				r.Phone = val
			}
			if r.HasData() {
				results <- r
			}
		}
	}()

	return results
}

func (s *WeLeakInfo) Name() string {
	return "weleakinfo"
}


func (s *WeLeakInfo) NeedsKey() bool {
	return true
}

func (s *WeLeakInfo) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

func (s *WeLeakInfo) RateLimit() int {
	return 5
}
