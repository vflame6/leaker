package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/vflame6/leaker/logger"
	"io"
	"net/http"
)

type ProxyNovaResponse struct {
	Count int      `json:"count"`
	Lines []string `json:"lines"`
}

type ProxyNova struct {
}

// Run function returns all results found with the service.
// ProxyNova does not filter by target type, so result filtering is applied downstream.
func (s *ProxyNova) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		// Fetch the first page to learn the total count
		// scanType is ignored because ProxyNova does not support scan types
		firstPage, err := s.fetchPage(ctx, target, 0, session)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}

		for _, line := range firstPage.Lines {
			results <- Result{Source: s.Name(), Value: line}
		}

		// Paginate if there are more results than the first page returned
		// COMMENTED OUT because proxynova does not allow to probe for other pages. Only first one is available for now.
		// Current proxynova response:
		//{
		//	"error": "You are limited to 100 results"
		//}
		// if firstPage.Count > len(firstPage.Lines) {
		//	logger.Debugf("ProxyNova: %d total results for %s, paginating", firstPage.Count, target)
		//	start := len(firstPage.Lines)
		//	for start < firstPage.Count {
		//		if ctx.Err() != nil {
		//			return
		//		}
		//		page, err := s.fetchPage(ctx, target, start, session)
		//		if err != nil {
		//			results <- Result{Source: s.Name(), Error: err}
		//			return
		//		}
		//		if len(page.Lines) == 0 {
		//			// no more results despite count suggesting otherwise
		//			break
		//		}
		//		for _, line := range page.Lines {
		//			results <- Result{Source: s.Name(), Value: line}
		//		}
		//		start += len(page.Lines)
		//	}
		//}
	}()

	return results
}

// fetchPage retrieves one page of results from ProxyNova starting at offset start.
func (s *ProxyNova) fetchPage(ctx context.Context, target string, start int, session *Session) (*ProxyNovaResponse, error) {
	url := fmt.Sprintf("https://api.proxynova.com/comb?query=%s&start=%d&limit=100", target, start)
	logger.Debugf("Sending a request in ProxyNova source for %s (start=%d)", target, start)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := session.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer session.DiscardHTTPResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	logger.Debugf("Response from ProxyNova source: status code [%d], size [%d]", resp.StatusCode, len(body))

	var response ProxyNovaResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse ProxyNova response: %s", string(body))
	}

	return &response, nil
}

// Name returns the name of the source
func (s *ProxyNova) Name() string {
	return "proxynova"
}

func (s *ProxyNova) IsDefault() bool {
	return true
}

func (s *ProxyNova) NeedsKey() bool {
	return false
}

func (s *ProxyNova) AddApiKeys(_ []string) {
	// no key needed
}

func (s *ProxyNova) RateLimit() int {
	// from https://www.proxynova.com/tools/comb/
	// "You are limited to about 100 requests per minute."
	return 90
}
