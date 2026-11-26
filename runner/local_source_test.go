package runner

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/vflame6/leaker/runner/sources"
)

// runLocal searches db through the local source and returns the usernames,
// emails and errors it produced.
func runLocal(t *testing.T, db *LeakerDB, target string, st sources.ScanType) ([]sources.Result, []error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	src := &sources.LocalDB{Lookup: db.Search}
	var results []sources.Result
	var errs []error
	_, err := src.Search(ctx, sources.Query{Target: target, Type: st}, nil, func(r sources.Result) {
		results = append(results, r)
	})
	if err != nil {
		errs = append(errs, err)
	}
	return results, errs
}

func seedDB(t *testing.T, rows ...sources.Result) *LeakerDB {
	t.Helper()
	db, err := OpenLeakerDB(tempDBPath(t), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for i := range rows {
		if err := db.Insert(&rows[i]); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return db
}

func TestLocalSource_HappyPath(t *testing.T) {
	db := seedDB(t,
		sources.Result{Source: "snusbase", Email: "jane@example.com", Password: "hunter2", Database: "ExampleDB"},
		sources.Result{Source: "snusbase", Email: "bob@example.com", Password: "x"},
	)
	results, errs := runLocal(t, db, "jane@example.com", sources.TypeEmail)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(results), results)
	}
	got := results[0]
	if got.Source != sources.LocalSourceName || got.Email != "jane@example.com" || got.Password != "hunter2" || got.Database != "ExampleDB" {
		t.Errorf("unexpected result %+v", got)
	}
}

func TestLocalSource_ErrorPath(t *testing.T) {
	db := seedDB(t, sources.Result{Email: "jane@example.com"})
	_ = db.db.Close() // the file is gone from under the runner

	results, errs := runLocal(t, db, "jane@example.com", sources.TypeEmail)
	if len(errs) == 0 {
		t.Fatalf("a failed query must produce an error result, got %+v", results)
	}
}

// TestLocalSource_SpecialCharacters checks that LIKE wildcards and quotes in a
// target are matched literally.
func TestLocalSource_SpecialCharacters(t *testing.T) {
	db := seedDB(t,
		sources.Result{Username: "jane_doe"},
		sources.Result{Username: "janeXdoe"},
		sources.Result{Username: "100%off"},
		sources.Result{Username: "1000ff"},
		sources.Result{Username: `back\slash`},
		sources.Result{Username: "o'brien"},
		sources.Result{Username: "a&b=c#d"},
		sources.Result{Username: "ключ café"},
	)
	cases := map[string][]string{
		"jane_doe":  {"jane_doe"},
		"100%":      {"100%off"},
		`k\s`:       {`back\slash`},
		"o'brien":   {"o'brien"},
		"a&b=c#d":   {"a&b=c#d"},
		"ключ café": {"ключ café"},
	}
	for target, want := range cases {
		results, errs := runLocal(t, db, target, sources.TypeKeyword)
		if len(errs) != 0 {
			t.Errorf("%q: unexpected errors %v", target, errs)
			continue
		}
		var got []string
		for _, r := range results {
			got = append(got, r.Username)
		}
		sort.Strings(got)
		if len(got) != len(want) || (len(got) > 0 && got[0] != want[0]) {
			t.Errorf("search %q matched %q, want %q", target, got, want)
		}
	}
}
