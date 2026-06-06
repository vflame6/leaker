package sources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestLeakRadarEmailSearchAutoUnlocksAndMapsCleartext(t *testing.T) {
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/search/email" {
			t.Errorf("path = %s, want /search/email", r.URL.Path)
		}
		if got := r.URL.Query().Get("auto_unlock"); got != "true" {
			t.Errorf("auto_unlock = %q, want true", got)
		}
		if got := r.URL.Query().Get("page_size"); got != "100" {
			t.Errorf("page_size = %q, want 100", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want bearer key", got)
		}

		var req leakRadarEmailSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Email != "user@example.com" {
			t.Errorf("email = %q, want user@example.com", req.Email)
		}
		if req.IsEmail == nil || !*req.IsEmail {
			t.Fatalf("is_email = %v, want true", req.IsEmail)
		}

		_, _ = w.Write([]byte(`{
			"items": [
				{
					"id": "locked",
					"username_masked": "u***@example.com",
					"unlocked": false
				},
				{
					"id": "clear",
					"username": "user@example.com",
					"password": "cleartext",
					"url": "https://example.com/login",
					"password_strength": 6,
					"status": "new",
					"added_at": "2026-06-01T12:00:00Z",
					"unlocked": true,
					"is_email": true
				}
			],
			"total": 2,
			"total_unlocked": 1,
			"page": 1,
			"page_size": 100,
			"auto_unlock_points_consumed": 1
		}`))
	}))
	defer server.Close()

	source := &LeakRadar{apiKeys: []string{"test-key"}, baseURL: server.URL}
	results := collectLeakRadarResults(source.Run(context.Background(), "user@example.com", TypeEmail, testSession(server.Client())))

	if !sawRequest {
		t.Fatal("server did not receive request")
	}
	if len(results) != 1 {
		t.Fatalf("expected only the unlocked result, got %#v", results)
	}
	result := results[0]
	if result.Email != "user@example.com" {
		t.Errorf("Email = %q", result.Email)
	}
	if result.Password != "cleartext" {
		t.Errorf("Password = %q", result.Password)
	}
	if result.URL != "https://example.com/login" {
		t.Errorf("URL = %q", result.URL)
	}
	if result.Extra["leak_id"] != "clear" {
		t.Errorf("leak_id extra = %q", result.Extra["leak_id"])
	}
	if result.Extra["password_strength"] != "6" {
		t.Errorf("password_strength extra = %q", result.Extra["password_strength"])
	}
}

func TestLeakRadarUsernameSearchSetsIsEmailFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req leakRadarEmailSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Email != "alice" {
			t.Errorf("email = %q, want alice", req.Email)
		}
		if req.IsEmail == nil || *req.IsEmail {
			t.Fatalf("is_email = %v, want false", req.IsEmail)
		}
		_, _ = w.Write([]byte(`{
			"items": [
				{"username": "alice", "password": "secret", "unlocked": true, "is_email": false}
			],
			"total": 1,
			"total_unlocked": 1,
			"page": 1,
			"page_size": 100
		}`))
	}))
	defer server.Close()

	source := &LeakRadar{apiKeys: []string{"test-key"}, baseURL: server.URL}
	results := collectLeakRadarResults(source.Run(context.Background(), "alice", TypeUsername, testSession(server.Client())))

	if len(results) != 1 {
		t.Fatalf("expected one result, got %#v", results)
	}
	if results[0].Username != "alice" {
		t.Errorf("Username = %q", results[0].Username)
	}
	if results[0].Email != "" {
		t.Errorf("Email = %q, want empty", results[0].Email)
	}
}

func TestLeakRadarKeywordAndPhoneSearchUseUsernameSearch(t *testing.T) {
	tests := []struct {
		name     string
		scanType ScanType
		target   string
	}{
		{
			name:     "keyword",
			scanType: TypeKeyword,
			target:   "alice",
		},
		{
			name:     "phone",
			scanType: TypePhone,
			target:   "+15551234567",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/search/email" {
					t.Errorf("path = %s, want /search/email", r.URL.Path)
				}
				if got := r.URL.Query().Get("auto_unlock"); got != "true" {
					t.Errorf("auto_unlock = %q, want true", got)
				}

				var req leakRadarEmailSearchRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if req.Email != tt.target {
					t.Errorf("email = %q, want %s", req.Email, tt.target)
				}
				if req.IsEmail == nil || *req.IsEmail {
					t.Fatalf("is_email = %v, want false", req.IsEmail)
				}

				_, _ = w.Write([]byte(`{
					"items": [
						{"username": "` + tt.target + `", "password": "secret", "unlocked": true, "is_email": false}
					],
					"total": 1,
					"total_unlocked": 1,
					"page": 1,
					"page_size": 100
				}`))
			}))
			defer server.Close()

			source := &LeakRadar{apiKeys: []string{"test-key"}, baseURL: server.URL}
			results := collectLeakRadarResults(source.Run(context.Background(), tt.target, tt.scanType, testSession(server.Client())))

			if len(results) != 1 {
				t.Fatalf("expected one result, got %#v", results)
			}
			if results[0].Username != tt.target {
				t.Errorf("Username = %q", results[0].Username)
			}
		})
	}
}

func TestLeakRadarEmailSearchAutoUnlocksEveryFetchedPage(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		if got := r.URL.Query().Get("auto_unlock"); got != "true" {
			t.Errorf("page %s auto_unlock = %q, want true", page, got)
		}

		switch page {
		case "1":
			_, _ = w.Write([]byte(`{
				"items": [
					{"username": "user@example.com", "password": "first", "unlocked": true, "is_email": true}
				],
				"total": 2,
				"total_unlocked": 1,
				"page": 1,
				"page_size": 1
			}`))
		case "2":
			_, _ = w.Write([]byte(`{
				"items": [
					{"username": "user@example.com", "password": "second", "unlocked": true, "is_email": true}
				],
				"total": 2,
				"total_unlocked": 2,
				"page": 2,
				"page_size": 1
			}`))
		default:
			t.Fatalf("unexpected page %s", page)
		}
	}))
	defer server.Close()

	source := &LeakRadar{apiKeys: []string{"test-key"}, baseURL: server.URL}
	results := collectLeakRadarResults(source.Run(context.Background(), "user@example.com", TypeEmail, testSession(server.Client())))

	if !reflect.DeepEqual(pages, []string{"1", "2"}) {
		t.Fatalf("pages = %#v, want [1 2]", pages)
	}
	if len(results) != 2 {
		t.Fatalf("expected two results, got %#v", results)
	}
	if results[0].Password != "first" || results[1].Password != "second" {
		t.Fatalf("passwords = %#v", results)
	}
}

func TestLeakRadarDomainSearchAutoUnlocksAndMapsCategory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/search/domain/example.com/all" {
			t.Errorf("path = %s, want /search/domain/example.com/all", r.URL.Path)
		}
		if got := r.URL.Query().Get("auto_unlock"); got != "true" {
			t.Errorf("auto_unlock = %q, want true", got)
		}
		if got := r.URL.Query().Get("page_size"); got != "1000" {
			t.Errorf("page_size = %q, want 1000", got)
		}
		_, _ = w.Write([]byte(`{
			"items": [
				{
					"id": "domain-clear",
					"username": "employee@example.com",
					"password": "cleartext",
					"url": "https://example.com/vpn",
					"category": "employees",
					"unlocked": true,
					"is_email": true
				}
			],
			"total": 1,
			"total_unlocked": 1,
			"page": 1,
			"page_size": 1000
		}`))
	}))
	defer server.Close()

	source := &LeakRadar{apiKeys: []string{"test-key"}, baseURL: server.URL}
	results := collectLeakRadarResults(source.Run(context.Background(), "example.com", TypeDomain, testSession(server.Client())))

	if len(results) != 1 {
		t.Fatalf("expected one result, got %#v", results)
	}
	if results[0].Email != "employee@example.com" {
		t.Errorf("Email = %q", results[0].Email)
	}
	if results[0].Extra["category"] != "employees" {
		t.Errorf("category extra = %q", results[0].Extra["category"])
	}
}

func testSession(client *http.Client) *Session {
	return &Session{Client: client}
}

func collectLeakRadarResults(ch <-chan Result) []Result {
	var results []Result
	for result := range ch {
		results = append(results, result)
	}
	return results
}
