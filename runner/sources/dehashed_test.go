package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/vflame6/leaker/internal/fakeapi"
)

// TestDeHashed_ReadsArrayFields: v2 entries carry every field as an array.
// The value holding the target comes first so the result filter sees it.
func TestDeHashed_ReadsArrayFields(t *testing.T) {
	srv := fakeapi.NewServer(t, fixtureHandler(t, "dehashed", map[string]string{"POST api.dehashed.com/v2/search": "search-v2.json"}))
	results, errs := runSource(t, withKeys(&DeHashed{}, testKey), "jane@example.com", TypeEmail, srv.Transport())
	for _, err := range errs {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Result{
		{Source: "dehashed", Email: "jane@example.com", Username: "jane", Password: "hunter2", Name: "Jane Doe",
			Phone: "+1 555 234 1096", IP: "203.0.113.7", URL: "https://login.example.com", Database: "ExampleDB",
			Extra: map[string]string{"dob": "1990-01-01"}},
		{Source: "dehashed", Email: "JANE@example.com", Hash: "5f4dcc3b5aa765d61d8327deb882cf99", Database: "Other",
			Extra: map[string]string{"email_2": "old@example.org"}},
	}
	if !reflect.DeepEqual(results, want) {
		t.Errorf("results differ\n got: %+v\nwant: %+v", results, want)
	}
}

// dehashedMock answers like DeHashed for a query matching total entries: it
// honours page and size and reports the balance.
func dehashedMock(t *testing.T, total, balance int) *fakeapi.Server {
	t.Helper()
	return fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req dehashedSearchRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("bad request body %s", body)
		}
		if req.Size < 1 || req.Size > 10000 || req.Page < 1 || req.Page*req.Size > 10000 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"pagination limit exceeded"}`)
			return
		}
		var entries []string
		for i := (req.Page - 1) * req.Size; i < total && i < req.Page*req.Size; i++ {
			entries = append(entries, fmt.Sprintf(`{"id":"%d","email":["user%d@example.com"],"password":["pw%d"],"database_name":"DB"}`, i, i, i))
		}
		_, _ = fmt.Fprintf(w, `{"balance":%d,"took":"9ms","total":%d,"entries":[%s]}`, balance, total, strings.Join(entries, ","))
	}))
}

// TestDeHashed_ReportsTotalAndBalance: the API's total and balance reach the
// stats, and stopping short of the total is a truncation.
func TestDeHashed_ReportsTotalAndBalance(t *testing.T) {
	srv := dehashedMock(t, 4213, 95)
	results, stats, err := searchSource(t, withKeys(&DeHashed{}, testKey), Query{Target: "example.com", Type: TypeDomain, MaxResults: 100}, srv.Transport())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 100 {
		t.Errorf("%d results, want one page of 100", len(results))
	}
	if stats.Total != 4213 || stats.CreditsLeft == nil || *stats.CreditsLeft != 95 || !stats.Truncated {
		t.Errorf("stats = %+v (credits %v), want total 4213, 95 credits left, truncated", stats, stats.CreditsLeft)
	}
}

// TestDeHashed_ErrorKinds: DeHashed answers 403 when credits run out and 400
// for a bad key.
func TestDeHashed_ErrorKinds(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   error
	}{
		{http.StatusForbidden, `{"error":"Insufficient Credits"}`, ErrQuota},
		{http.StatusBadRequest, `{"error":"There was an issue with authentication"}`, ErrAuth},
	}
	for _, c := range cases {
		srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		}))
		_, errs := runSource(t, withKeys(&DeHashed{}, testKey), "jane@example.com", TypeEmail, srv.Transport())
		if len(errs) != 1 || !errors.Is(errs[0], c.kind) {
			t.Errorf("status %d: errors %v, want %v", c.status, errs, c.kind)
			continue
		}
		if c.kind == ErrQuota && errors.Is(errs[0], ErrAuth) {
			t.Errorf("status %d: out of credits is not an authentication failure", c.status)
		}
		if !strings.Contains(errs[0].Error(), "returned status") {
			t.Errorf("status %d: error %q lost the API's answer", c.status, errs[0])
		}
	}
}
