package sources

import "testing"

func TestResult_Checksum_Stable(t *testing.T) {
	r := Result{
		Email:    "user@example.com",
		Username: "user",
		Password: "hunter2",
	}
	first := r.Checksum()
	second := r.Checksum()
	if first != second {
		t.Fatalf("Checksum not stable: %q vs %q", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("expected 64 hex chars, got %d: %q", len(first), first)
	}
}

func TestResult_Checksum_ExcludesDatabase(t *testing.T) {
	a := Result{Email: "user@example.com", Password: "p", Database: "DBA"}
	b := Result{Email: "user@example.com", Password: "p", Database: "DBB"}
	if a.Checksum() != b.Checksum() {
		t.Fatalf("Database should not affect checksum: %q vs %q", a.Checksum(), b.Checksum())
	}
}

func TestResult_Checksum_ExcludesSource(t *testing.T) {
	a := Result{Email: "user@example.com", Password: "p", Source: "snusbase"}
	b := Result{Email: "user@example.com", Password: "p", Source: "dehashed"}
	if a.Checksum() != b.Checksum() {
		t.Fatalf("Source should not affect checksum: %q vs %q", a.Checksum(), b.Checksum())
	}
}

func TestResult_Checksum_IncludesExtra(t *testing.T) {
	a := Result{Email: "user@example.com"}
	a.SetExtra("k", "v1")
	b := Result{Email: "user@example.com"}
	b.SetExtra("k", "v2")
	if a.Checksum() == b.Checksum() {
		t.Fatalf("Extra values should affect checksum but didn't")
	}
}

func TestResult_Checksum_ExtraKeyOrderStable(t *testing.T) {
	a := Result{Email: "user@example.com"}
	a.SetExtra("a", "1")
	a.SetExtra("b", "2")
	b := Result{Email: "user@example.com"}
	b.SetExtra("b", "2")
	b.SetExtra("a", "1")
	if a.Checksum() != b.Checksum() {
		t.Fatalf("Extra key insertion order should not affect checksum: %q vs %q", a.Checksum(), b.Checksum())
	}
}

func TestResult_Checksum_EmptyVsPresent(t *testing.T) {
	empty := Result{Username: "user"}
	present := Result{Email: "x", Username: "user"}
	if empty.Checksum() == present.Checksum() {
		t.Fatalf("empty vs non-empty email should differ in checksum")
	}
}

func TestResult_TrimSpaces(t *testing.T) {
	r := Result{
		Source:   "  snusbase  ",
		Email:    "  user@example.com\t",
		Username: " alice ",
		Password: "  keep me  ",
		Hash:     "\tabc123\n",
		Salt:     "  keep me too  ",
		IP:       " 10.0.0.1 ",
		Phone:    "  15551234567 ",
		Name:     "\tAlice Example ",
		Database: "  ExampleDB  ",
		URL:      " https://example.com/leak ",
	}
	r.SetExtra("k1", "  v1  ")
	r.SetExtra("k2", "\tv2\n")

	r.TrimSpaces()

	if r.Source != "snusbase" {
		t.Errorf("Source not trimmed: %q", r.Source)
	}
	if r.Email != "user@example.com" {
		t.Errorf("Email not trimmed: %q", r.Email)
	}
	if r.Username != "alice" {
		t.Errorf("Username not trimmed: %q", r.Username)
	}
	if r.Password != "  keep me  " {
		t.Errorf("Password should NOT be trimmed: %q", r.Password)
	}
	if r.Hash != "abc123" {
		t.Errorf("Hash not trimmed: %q", r.Hash)
	}
	if r.Salt != "  keep me too  " {
		t.Errorf("Salt should NOT be trimmed: %q", r.Salt)
	}
	if r.IP != "10.0.0.1" {
		t.Errorf("IP not trimmed: %q", r.IP)
	}
	if r.Phone != "15551234567" {
		t.Errorf("Phone not trimmed: %q", r.Phone)
	}
	if r.Name != "Alice Example" {
		t.Errorf("Name not trimmed: %q", r.Name)
	}
	if r.Database != "ExampleDB" {
		t.Errorf("Database not trimmed: %q", r.Database)
	}
	if r.URL != "https://example.com/leak" {
		t.Errorf("URL not trimmed: %q", r.URL)
	}
	if r.Extra["k1"] != "v1" {
		t.Errorf("Extra[k1] not trimmed: %q", r.Extra["k1"])
	}
	if r.Extra["k2"] != "v2" {
		t.Errorf("Extra[k2] not trimmed: %q", r.Extra["k2"])
	}
}

// Fields that contain only whitespace should become empty strings so that
// HasData() downstream treats them as missing. This is the motivating bug:
// sources occasionally return " " for fields like Name, which should not
// print as "name:".
func TestResult_TrimSpaces_WhitespaceOnlyBecomesEmpty(t *testing.T) {
	r := Result{
		Email: "real@example.com",
		Name:  "   ",
		URL:   "\t\n ",
	}
	r.TrimSpaces()
	if r.Name != "" {
		t.Errorf("whitespace-only Name should become empty, got %q", r.Name)
	}
	if r.URL != "" {
		t.Errorf("whitespace-only URL should become empty, got %q", r.URL)
	}
	if r.Email != "real@example.com" {
		t.Errorf("Email unexpectedly changed: %q", r.Email)
	}
}

// A Result whose only non-empty field was whitespace should register as
// having no data after TrimSpaces runs.
func TestResult_TrimSpaces_CollapsesToNoData(t *testing.T) {
	r := Result{Name: "   "}
	if !r.HasData() {
		t.Fatal("precondition: Name=\"   \" should register as data before trim")
	}
	r.TrimSpaces()
	if r.HasData() {
		t.Error("after trim, whitespace-only Result should have no data")
	}
}

// TrimSpaces must be safe on a Result with a nil Extra map.
func TestResult_TrimSpaces_NilExtra(t *testing.T) {
	r := Result{Email: " x@y.com "}
	r.TrimSpaces() // must not panic
	if r.Email != "x@y.com" {
		t.Errorf("Email not trimmed: %q", r.Email)
	}
}

func TestResult_Checksum_Cached(t *testing.T) {
	r := Result{Email: "user@example.com", Password: "p"}
	first := r.Checksum()
	// Mutate a field after first call — cached value should persist.
	r.Password = "different"
	second := r.Checksum()
	if first != second {
		t.Fatalf("Checksum should be cached after first call: %q vs %q", first, second)
	}
}
