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

type DeHashed struct {
	apiKeys []string
}

type dehashedSearchRequest struct {
	Query  string `json:"query"`
	Page   int    `json:"page"`
	Size   int    `json:"size"`
	DeDupe bool   `json:"de_dupe"`
}

type dehashedSearchResponse struct {
	Balance int             `json:"balance"`
	Entries []dehashedEntry `json:"entries"`
	Total   int             `json:"total"`
}

type dehashedEntry struct {
	Email          string `json:"email"`
	IPAddress      string `json:"ip_address"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	HashedPassword string `json:"hashed_password"`
	Name           string `json:"name"`
	Phone          string `json:"phone"`
	DatabaseName   string `json:"database_name"`
}

func (s *DeHashed) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name())
		if randomApiKey == "" {
			return
		}

		// Build query with field prefix based on scan type
		var query string
		switch scanType {
		case TypeEmail:
			query = "email:" + target
		case TypeUsername:
			query = "username:" + target
		case TypeDomain:
			query = "email:*@" + target
		case TypeKeyword:
			query = target
		case TypePhone:
			query = "phone:" + target
		}

		searchReq := dehashedSearchRequest{
			Query:  query,
			Page:   1,
			Size:   100,
			DeDupe: true,
		}

		body, err := json.Marshal(searchReq)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.dehashed.com/v2/search",
			bytes.NewReader(body))
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		req.Header.Set("Dehashed-Api-Key", randomApiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in DeHashed source for %s", target)
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
		logger.Debugf("Response from DeHashed source: status code [%d], size [%d]", resp.StatusCode, len(respBody))

		if resp.StatusCode != http.StatusOK {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("DeHashed returned status %d: %s", resp.StatusCode, string(respBody)),
			}
			return
		}

		var response dehashedSearchResponse
		if err := json.Unmarshal(respBody, &response); err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		for _, entry := range response.Entries {
			var parts []string
			if entry.Email != "" {
				parts = append(parts, "email:"+entry.Email)
			}
			if entry.Username != "" {
				parts = append(parts, "username:"+entry.Username)
			}
			if entry.Password != "" {
				parts = append(parts, "password:"+entry.Password)
			}
			if entry.HashedPassword != "" {
				parts = append(parts, "hash:"+entry.HashedPassword)
			}
			if entry.Name != "" {
				parts = append(parts, "name:"+entry.Name)
			}
			if entry.Phone != "" {
				parts = append(parts, "phone:"+entry.Phone)
			}
			if entry.IPAddress != "" {
				parts = append(parts, "ip:"+entry.IPAddress)
			}
			if entry.DatabaseName != "" {
				parts = append(parts, "database:"+entry.DatabaseName)
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

func (s *DeHashed) Name() string {
	return "dehashed"
}

func (s *DeHashed) IsDefault() bool {
	return false
}

func (s *DeHashed) NeedsKey() bool {
	return true
}

func (s *DeHashed) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

func (s *DeHashed) RateLimit() int {
	return 10
}
