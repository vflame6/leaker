package cmd

import "testing"

func TestSearchLimits(t *testing.T) {
	cases := []struct {
		name                      string
		maxResults, maxRequests   int
		all, maxRequestsTyped     bool
		wantResults, wantRequests int
		wantErr                   bool
	}{
		{"defaults", 1000, 10, false, false, 1000, 10, false},
		{"--all lifts both", 1000, 10, true, false, 0, 0, false},
		{"--all keeps a typed --max-requests", 1000, 5, true, true, 0, 5, false},
		{"--max-results 0", 0, 3, false, false, 0, 0, true},
		{"--max-requests 0", 1000, 0, false, true, 0, 0, true},
	}
	for _, c := range cases {
		limits, err := searchLimits(c.maxResults, c.maxRequests, c.all, c.maxRequestsTyped)
		if (err != nil) != c.wantErr || limits.maxResults != c.wantResults || limits.maxRequests != c.wantRequests {
			t.Errorf("%s: got %+v, %v; want %d, %d, error %v", c.name, limits, err, c.wantResults, c.wantRequests, c.wantErr)
		}
		if err == nil && limits.maxRequestsTyped != c.maxRequestsTyped {
			t.Errorf("%s: maxRequestsTyped %v, want %v (sources use their own default otherwise)", c.name, limits.maxRequestsTyped, c.maxRequestsTyped)
		}
	}
}

// TestMaxRequestsDefaultsToTen: without --max-requests a source sends up to
// ten search requests per target.
func TestMaxRequestsDefaultsToTen(t *testing.T) {
	parser, err := newParser()
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range parser.Model.Flags {
		if flag.Name == "max-requests" {
			if flag.Default != "10" {
				t.Errorf("--max-requests defaults to %q, want 10", flag.Default)
			}
			return
		}
	}
	t.Fatal("no --max-requests flag")
}
