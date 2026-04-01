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

type Snusbase struct {
	apiKeys []string
}

type snusbaseSearchRequest struct {
	Terms    []string    `json:"terms"`
	Types    []string    `json:"types"`
	Wildcard bool        `json:"wildcard,omitempty"`
	GroupBy  interface{} `json:"group_by,omitempty"`
	Tables   []string    `json:"tables,omitempty"`
}

type snusbaseSearchResponse struct {
	Took    float64                             `json:"took"`
	Size    int                                 `json:"size"`
	Results map[string][]map[string]interface{} `json:"results"`
}

// snusbaseHashLookupResponse handles hash-lookup which returns a flat array.
type snusbaseHashLookupResponse struct {
	Took    float64                  `json:"took"`
	Size    int                      `json:"size"`
	Results []map[string]interface{} `json:"results"`
}

// snusbaseIPWhoisResponse handles ip-whois which returns IP -> object map.
type snusbaseIPWhoisResponse struct {
	Took    float64                           `json:"took"`
	Size    int                               `json:"size"`
	Results map[string]map[string]interface{} `json:"results"`
}

// snusbasePost sends a POST request to a Snusbase API endpoint.
func (s *Snusbase) snusbasePost(ctx context.Context, session *Session, apiKey, url string, payload interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Auth", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := session.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer session.DiscardHTTPResponse(resp)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Snusbase %s returned status %d: %s", url, resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func (s *Snusbase) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		apiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
		if apiKey == "" {
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

		// --- Step 1: Main search ---
		logger.Debugf("Snusbase: searching for %s", target)
		searchBody, err := s.snusbasePost(ctx, session, apiKey,
			"https://api.snusbase.com/data/search",
			snusbaseSearchRequest{
				Terms: []string{target},
				Types: searchTypes,
			})
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		var searchResp snusbaseSearchResponse
		if err := json.Unmarshal(searchBody, &searchResp); err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		// Collect results and gather hashes + IPs for enrichment
		var pendingResults []Result
		hashSet := make(map[string]bool)
		ipSet := make(map[string]bool)
		var dbNames []string

		for dbName, records := range searchResp.Results {
			dbNames = append(dbNames, dbName)
			for _, record := range records {
				r := Result{
					Source:   s.Name(),
					Database: dbName,
				}
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
					hashSet[val] = true
				}
				if val, ok := record["lastip"].(string); ok && val != "" {
					r.IP = val
					ipSet[val] = true
				}
				if val, ok := record["name"].(string); ok && val != "" {
					r.Name = val
				}
				if val, ok := record["salt"].(string); ok && val != "" {
					r.SetExtra("salt", val)
				}
				if r.HasData() {
					pendingResults = append(pendingResults, r)
				}
			}
		}

		// --- Step 2: Combo-lookup (reveals plaintext passwords from combolists) ---
		// Combo-lookup always uses "username" type — emails are stored as
		// the username field in combolists (user:pass format).
		logger.Debugf("Snusbase: combo-lookup for %s", target)
		comboBody, err := s.snusbasePost(ctx, session, apiKey,
			"https://api.snusbase.com/tools/combo-lookup",
			snusbaseSearchRequest{
				Terms:   []string{target},
				Types:   []string{"username"},
				GroupBy: "db",
			})
		if err != nil {
			logger.Debugf("Snusbase combo-lookup error: %v", err)
		} else {
			var comboResp snusbaseSearchResponse
			if err := json.Unmarshal(comboBody, &comboResp); err == nil {
				for dbName, records := range comboResp.Results {
					for _, record := range records {
						r := Result{
							Source:   s.Name(),
							Database: dbName,
						}
						if val, ok := record["username"].(string); ok && val != "" {
							r.Username = val
						}
						if val, ok := record["email"].(string); ok && val != "" {
							r.Email = val
						}
						if val, ok := record["password"].(string); ok && val != "" {
							r.Password = val
						}
						if r.HasData() {
							pendingResults = append(pendingResults, r)
						}
					}
				}
			}
		}

		// --- Step 3: Hash-lookup (crack hashes found in search results) ---
		if len(hashSet) > 0 {
			hashes := make([]string, 0, len(hashSet))
			for h := range hashSet {
				hashes = append(hashes, h)
			}
			logger.Debugf("Snusbase: hash-lookup for %d hash(es)", len(hashes))
			hashBody, err := s.snusbasePost(ctx, session, apiKey,
				"https://api.snusbase.com/tools/hash-lookup",
				map[string]interface{}{
					"terms":    hashes,
					"types":    []string{"hash"},
					"group_by": false,
				})
			if err != nil {
				logger.Debugf("Snusbase hash-lookup error: %v", err)
			} else {
				var hashResp snusbaseHashLookupResponse
				if err := json.Unmarshal(hashBody, &hashResp); err == nil {
					// Build hash → password map
					crackedHashes := make(map[string]string)
					for _, record := range hashResp.Results {
						h, _ := record["hash"].(string)
						p, _ := record["password"].(string)
						if h != "" && p != "" {
							crackedHashes[h] = p
						}
					}
					// Enrich pending results: if a result has a hash but no password, fill it in
					for i := range pendingResults {
						if pendingResults[i].Hash != "" && pendingResults[i].Password == "" {
							if cracked, ok := crackedHashes[pendingResults[i].Hash]; ok {
								pendingResults[i].Password = cracked
							}
						}
					}
				}
			}
		}

		// --- Step 4: IP-whois (enrich IPs with geolocation) ---
		if len(ipSet) > 0 {
			ips := make([]string, 0, len(ipSet))
			for ip := range ipSet {
				ips = append(ips, ip)
			}
			logger.Debugf("Snusbase: ip-whois for %d IP(s)", len(ips))
			whoisBody, err := s.snusbasePost(ctx, session, apiKey,
				"https://api.snusbase.com/tools/ip-whois",
				map[string]interface{}{
					"terms": ips,
				})
			if err != nil {
				logger.Debugf("Snusbase ip-whois error: %v", err)
			} else {
				var whoisResp snusbaseIPWhoisResponse
				if err := json.Unmarshal(whoisBody, &whoisResp); err == nil {
					// Build IP → geo string map
					ipGeo := make(map[string]string)
					for ip, info := range whoisResp.Results {
						city, _ := info["city"].(string)
						country, _ := info["country"].(string)
						isp, _ := info["isp"].(string)
						geo := ""
						if city != "" && country != "" {
							geo = city + ", " + country
						} else if country != "" {
							geo = country
						}
						if isp != "" {
							if geo != "" {
								geo += " (" + isp + ")"
							} else {
								geo = isp
							}
						}
						if geo != "" {
							ipGeo[ip] = geo
						}
					}
					// Enrich pending results with geo data
					for i := range pendingResults {
						if pendingResults[i].IP != "" {
							if geo, ok := ipGeo[pendingResults[i].IP]; ok {
								pendingResults[i].SetExtra("ip_geo", geo)
							}
						}
					}
				}
			}
		}

		// --- Emit all enriched results ---
		for _, r := range pendingResults {
			results <- r
		}
	}()

	return results
}

func (s *Snusbase) Name() string {
	return "snusbase"
}

func (s *Snusbase) UsesKey() bool {
	return true
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
