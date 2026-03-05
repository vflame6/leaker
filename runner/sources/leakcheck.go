package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
	"io"
	"net/http"
)

type LeakCheck struct {
	apiKeys []string
}

// Run function returns all subdomains found with the service
func (s *LeakCheck) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer func() {
			close(results)
		}()

		randomApiKey := utils.PickRandom(s.apiKeys, s.Name())
		// skip target if no keys are provided
		if randomApiKey == "" {
			return
		}

		var url string
		var response map[string]interface{}

		switch scanType {
		case TypeEmail:
			url = fmt.Sprintf("https://leakcheck.io/api/v2/query/%s?type=email", target)
		case TypeUsername:
			url = fmt.Sprintf("https://leakcheck.io/api/v2/query/%s?type=username", target)
		case TypeDomain:
			url = fmt.Sprintf("https://leakcheck.io/api/v2/query/%s?type=domain", target)
		case TypeKeyword:
			url = fmt.Sprintf("https://leakcheck.io/api/v2/query/%s?type=keyword", target)
		case TypePhone:
			url = fmt.Sprintf("https://leakcheck.io/api/v2/query/%s?type=phone", target)
		}

		// prepare request with custom headers
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			results <- Result{
				Source: s.Name(),
				Error:  err,
			}
			return
		}
		req.Header.Add("X-API-Key", randomApiKey)
		req.Header.Add("Accept", "application/json")

		// perform the request
		logger.Debugf("Sending a request in LeakCheck source for %s", target)
		resp, err := session.Client.Do(req)
		if err != nil {
			results <- Result{
				Source: s.Name(),
				Error:  err,
			}
			return
		}
		defer session.DiscardHTTPResponse(resp)

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			results <- Result{
				Source: s.Name(),
				Error:  err,
			}
			return
		}
		logger.Debugf("Response from LeakCheck source: status code [%d], size [%d]", resp.StatusCode, len(body))

		err = json.Unmarshal(body, &response)
		if err != nil {
			results <- Result{
				Source: s.Name(),
				Error:  err,
			}
			return
		}

		success, ok := response["success"].(bool)
		if !ok || !success {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("failed to parse LeakCheck response: %s", string(body)),
			}
			return
		}
		found, ok := response["found"].(float64)
		if !ok {
			results <- Result{
				Source: s.Name(),
				Error:  fmt.Errorf("failed to parse LeakCheck response: %s", string(body)),
			}
			return
		}
		foundInt := int(found)
		if foundInt > 0 {
			jsonResults, ok := response["result"].([]interface{})
			if !ok {
				results <- Result{
					Source: s.Name(),
					Error:  fmt.Errorf("failed to parse LeakCheck response: %s", string(body)),
				}
				return
			}
			for _, jsonResult := range jsonResults {
				parseResult := jsonResult.(map[string]interface{})
				jsonFields, ok := parseResult["fields"].([]interface{})
				if !ok {
					continue
				}

				r := Result{Source: s.Name()}
				for _, jsonField := range jsonFields {
					field := jsonField.(string)
					val, ok := parseResult[field].(string)
					if !ok || val == "" {
						continue
					}
					switch field {
					case "email":
						r.Email = val
					case "username":
						r.Username = val
					case "password":
						r.Password = val
					case "hash":
						r.Hash = val
					case "ip":
						r.IP = val
					case "phone":
						r.Phone = val
					case "name", "first_name", "last_name":
						if r.Name != "" {
							r.Name += " " + val
						} else {
							r.Name = val
						}
					default:
						r.SetExtra(field, val)
					}
				}
				if r.HasData() {
					results <- r
				}
			}
		}
	}()

	return results
}

// Name returns the name of the source
func (s *LeakCheck) Name() string {
	return "leakcheck"
}


func (s *LeakCheck) NeedsKey() bool {
	return true
}

func (s *LeakCheck) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

func (s *LeakCheck) RateLimit() int {
	// from https://wiki.leakcheck.io/en/api/api-v2-pro
	// "By default, the LeakCheck Pro API is limited to 3 requests per second on any plan.
	// You can increase this limit in the settings."
	return 3
}
