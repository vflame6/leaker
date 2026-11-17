package sources

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func collectResults(t *testing.T, results <-chan Result) []Result {
	t.Helper()
	var out []Result
	for result := range results {
		out = append(out, result)
	}
	return out
}

func TestExposeEmailSearchMapsPerson(t *testing.T) {
	var gotKey, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{
			"data": {
				"id": 1,
				"names": [{"first_name": "jane", "middle_name": null, "last_name": "doe"}],
				"emails": [{"address": "jane.doe@example.com"}],
				"phones": [{"number": "+12025550123"}],
				"profiles": [{"username": "janedoe", "url": "linkedin.com/in/janedoe", "network": "linkedin"}],
				"locations": [{"city": "san francisco", "state": "ca", "country": "united states"}],
				"experiences": [{"company_name": "acme inc"}]
			}
		}`)
	}))
	defer server.Close()

	source := &Expose{apiKeys: []string{"test-key"}, baseURL: server.URL}
	results := collectResults(t, source.Run(context.Background(), "jane.doe@example.com", TypeEmail, &Session{Client: server.Client()}))

	if gotKey != "test-key" {
		t.Fatalf("x-api-key = %q", gotKey)
	}
	if !strings.Contains(gotQuery, "type=person") || !strings.Contains(gotQuery, "email=jane.doe%40example.com") {
		t.Fatalf("query = %q", gotQuery)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	result := results[0]
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if result.Source != "expose" || result.Database != "Expose" {
		t.Fatalf("source metadata = %+v", result)
	}
	if result.Email != "jane.doe@example.com" || result.Name != "jane doe" || result.Phone != "+12025550123" {
		t.Fatalf("identity = %+v", result)
	}
	if result.Username != "janedoe" || result.URL != "https://linkedin.com/in/janedoe" {
		t.Fatalf("profile = %+v", result)
	}
	if result.Password != "" {
		t.Fatalf("password should stay empty, got %q", result.Password)
	}
	if result.Extra["company"] != "acme inc" || result.Extra["network"] != "linkedin" || result.Extra["location"] != "san francisco, ca, united states" {
		t.Fatalf("extra = %+v", result.Extra)
	}
}

func TestExposePhoneSearchEncodesQuery(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"data":{"id":2,"phones":[{"number":"+14155550100"}]}}`)
	}))
	defer server.Close()

	source := &Expose{apiKeys: []string{"test-key"}, baseURL: server.URL}
	results := collectResults(t, source.Run(context.Background(), "+14155550100", TypePhone, &Session{Client: server.Client()}))

	if !strings.Contains(gotQuery, "type=person") || !strings.Contains(gotQuery, "phone=%2B14155550100") {
		t.Fatalf("query = %q", gotQuery)
	}
	if len(results) != 1 || results[0].Phone != "+14155550100" {
		t.Fatalf("results = %+v", results)
	}
}

func TestExposeDomainSearchMapsCompany(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{
			"data": {
				"id": "co_1",
				"domain": "acme.com",
				"names": [{"first_name": "acme inc"}],
				"websites": [{"url": "https://www.acme.com"}]
			}
		}`)
	}))
	defer server.Close()

	source := &Expose{apiKeys: []string{"test-key"}, baseURL: server.URL}
	results := collectResults(t, source.Run(context.Background(), "acme.com", TypeDomain, &Session{Client: server.Client()}))

	if !strings.Contains(gotQuery, "type=company") || !strings.Contains(gotQuery, "domain=acme.com") {
		t.Fatalf("query = %q", gotQuery)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	if results[0].Name != "acme inc" || results[0].URL != "https://www.acme.com" {
		t.Fatalf("result = %+v", results[0])
	}
	if !results[0].Contains("acme.com") {
		t.Fatal("domain result would be filtered out")
	}
}

func TestExposeAcceptsWebsiteString(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"domain":"acme.com","websites":["https://acme.com"]}}`)
	}))
	defer server.Close()

	source := &Expose{apiKeys: []string{"test-key"}, baseURL: server.URL}
	results := collectResults(t, source.Run(context.Background(), "acme.com", TypeDomain, &Session{Client: server.Client()}))
	if len(results) != 1 || results[0].URL != "https://acme.com" {
		t.Fatalf("results = %+v", results)
	}
}

func TestExposeEmptyAndList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "empty%40example.com") {
			_, _ = io.WriteString(w, `{"data":{}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":1,"emails":[{"address":"a@example.com"}]},{"id":2,"emails":[{"address":"b@example.com"}]}]}`)
	}))
	defer server.Close()

	source := &Expose{apiKeys: []string{"test-key"}, baseURL: server.URL}
	empty := collectResults(t, source.Run(context.Background(), "empty@example.com", TypeEmail, &Session{Client: server.Client()}))
	if len(empty) != 0 {
		t.Fatalf("empty search returned %+v", empty)
	}

	list := collectResults(t, source.Run(context.Background(), "example.com", TypeDomain, &Session{Client: server.Client()}))
	if len(list) != 2 {
		t.Fatalf("got %d list results", len(list))
	}
}

func TestExposeRejectsMissingKeyAndUnsupportedTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.RawQuery)
	}))
	defer server.Close()

	source := &Expose{baseURL: server.URL}
	if results := collectResults(t, source.Run(context.Background(), "a@example.com", TypeEmail, &Session{Client: server.Client()})); len(results) != 0 {
		t.Fatalf("missing key returned %+v", results)
	}
	keyed := &Expose{apiKeys: []string{"test-key"}, baseURL: server.URL}
	if results := collectResults(t, keyed.Run(context.Background(), "someone", TypeUsername, &Session{Client: server.Client()})); len(results) != 0 {
		t.Fatalf("username returned %+v", results)
	}
	if results := collectResults(t, keyed.Run(context.Background(), "keyword", TypeKeyword, &Session{Client: server.Client()})); len(results) != 0 {
		t.Fatalf("keyword returned %+v", results)
	}
}

func TestExposeHTTPErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("email") {
		case "denied@example.com":
			w.WriteHeader(http.StatusUnauthorized)
		case "broke@example.com":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"message":"upstream down"}`)
		default:
			w.WriteHeader(http.StatusPaymentRequired)
		}
	}))
	defer server.Close()

	source := &Expose{apiKeys: []string{"test-key"}, baseURL: server.URL}
	session := &Session{Client: server.Client()}

	denied := collectResults(t, source.Run(context.Background(), "denied@example.com", TypeEmail, session))
	if len(denied) != 1 || denied[0].Error == nil || !strings.Contains(denied[0].Error.Error(), "rejected the API key") {
		t.Fatalf("401 = %+v", denied)
	}
	broke := collectResults(t, source.Run(context.Background(), "broke@example.com", TypeEmail, session))
	if len(broke) != 1 || broke[0].Error == nil || !strings.Contains(broke[0].Error.Error(), "502") {
		t.Fatalf("502 = %+v", broke)
	}
	billing := collectResults(t, source.Run(context.Background(), "empty@example.com", TypeEmail, session))
	if len(billing) != 1 || billing[0].Error == nil || !strings.Contains(billing[0].Error.Error(), "out of credits") {
		t.Fatalf("402 = %+v", billing)
	}
}
