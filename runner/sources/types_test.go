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
