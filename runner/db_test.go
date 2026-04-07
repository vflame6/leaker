package runner

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/vflame6/leaker/runner/sources"
)

func tempDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "leaker.db")
}

func TestOpenLeakerDB_CreatesFile(t *testing.T) {
	path := tempDBPath(t)
	db, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("OpenLeakerDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	// sanity: both tables exist
	for _, table := range []string{"leaker_meta", "leaks"} {
		var name string
		row := db.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table)
		if err := row.Scan(&name); err != nil {
			t.Fatalf("expected table %q to exist: %v", table, err)
		}
	}

	// leaker_meta has schema_version and schema_hash rows
	var version, hash string
	if err := db.db.QueryRow("SELECT value FROM leaker_meta WHERE key='schema_version'").Scan(&version); err != nil {
		t.Fatalf("schema_version row missing: %v", err)
	}
	if version != "1" {
		t.Errorf("expected schema_version='1', got %q", version)
	}
	if err := db.db.QueryRow("SELECT value FROM leaker_meta WHERE key='schema_hash'").Scan(&hash); err != nil {
		t.Fatalf("schema_hash row missing: %v", err)
	}
	if len(hash) != 64 {
		t.Errorf("expected 64-char sha256 hex schema_hash, got %d: %q", len(hash), hash)
	}
}

func TestOpenLeakerDB_ExistingValid(t *testing.T) {
	path := tempDBPath(t)

	db1, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// insert one row to verify contents survive reopen
	if err := db1.Insert(&sources.Result{Email: "a@b.com", Password: "p"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	_ = db1.Close()

	db2, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	var count int
	if err := db2.db.QueryRow("SELECT COUNT(*) FROM leaks").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after reopen, got %d", count)
	}
}

func TestOpenLeakerDB_SchemaMismatch(t *testing.T) {
	path := tempDBPath(t)
	db, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Corrupt the schema_hash
	if _, err := db.db.Exec("UPDATE leaker_meta SET value='deadbeef' WHERE key='schema_hash'"); err != nil {
		t.Fatalf("mutate meta: %v", err)
	}
	_ = db.Close()

	_, err = OpenLeakerDB(path, true)
	if err == nil {
		t.Fatal("expected schema mismatch error, got nil")
	}
}

func TestOpenLeakerDB_MissingMetaTable(t *testing.T) {
	path := tempDBPath(t)
	// Create a SQLite file with a leaks table but NO leaker_meta.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec("CREATE TABLE leaks (x INTEGER)"); err != nil {
		t.Fatalf("create leaks: %v", err)
	}
	_ = raw.Close()

	_, err = OpenLeakerDB(path, true)
	if err == nil {
		t.Fatal("expected missing-meta error, got nil")
	}
}

func TestOpenLeakerDB_ReadOnly(t *testing.T) {
	path := tempDBPath(t)
	db1, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := db1.Insert(&sources.Result{Email: "seed@x.com"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = db1.Close()

	db2, err := OpenLeakerDB(path, false)
	if err != nil {
		t.Fatalf("read-only open: %v", err)
	}
	defer func() { _ = db2.Close() }()

	// Insert is a no-op
	if err := db2.Insert(&sources.Result{Email: "another@x.com"}); err != nil {
		t.Fatalf("no-op insert should not error: %v", err)
	}
	var count int
	if err := db2.db.QueryRow("SELECT COUNT(*) FROM leaks").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row (no write), got %d", count)
	}
}

func TestLeakerDB_Insert_Dedup(t *testing.T) {
	path := tempDBPath(t)
	db, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	r := &sources.Result{Email: "a@b.com", Password: "p"}
	if err := db.Insert(r); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if err := db.Insert(r); err != nil {
		t.Fatalf("insert 2 (dup): %v", err)
	}
	var count int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM leaks").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after dup insert, got %d", count)
	}

	// Different result → new row
	if err := db.Insert(&sources.Result{Email: "a@b.com", Password: "q"}); err != nil {
		t.Fatalf("insert 3: %v", err)
	}
	if err := db.db.QueryRow("SELECT COUNT(*) FROM leaks").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows, got %d", count)
	}
}

func TestLeakerDB_Insert_AllFields(t *testing.T) {
	path := tempDBPath(t)
	db, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	original := &sources.Result{
		Source:   "snusbase",
		Email:    "alice@example.com",
		Username: "alice",
		Password: "hunter2",
		Hash:     "abcdef",
		Salt:     "salted",
		IP:       "10.0.0.1",
		Phone:    "15551234567",
		Name:     "Alice Example",
		Database: "ExampleDB_2024",
		URL:      "https://example.com/leak",
	}
	original.SetExtra("k1", "v1")
	original.SetExtra("k2", "v2")

	if err := db.Insert(original); err != nil {
		t.Fatalf("insert: %v", err)
	}

	results := collectSearch(t, db, "alice@example.com", sources.TypeEmail)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	got := results[0]

	if got.Email != original.Email || got.Username != original.Username ||
		got.Password != original.Password || got.Hash != original.Hash ||
		got.Salt != original.Salt || got.IP != original.IP ||
		got.Phone != original.Phone || got.Name != original.Name ||
		got.Database != original.Database || got.URL != original.URL {
		t.Errorf("field round-trip mismatch.\n got:  %+v\n want: %+v", got, original)
	}
	if got.Extra["k1"] != "v1" || got.Extra["k2"] != "v2" {
		t.Errorf("Extra round-trip mismatch: %+v", got.Extra)
	}
}

func TestLeakerDB_Search_ColumnMapping(t *testing.T) {
	path := tempDBPath(t)
	db, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Seed: put a unique token in each column, and one unrelated row.
	rows := []*sources.Result{
		{Email: "tok-email@x.com"},
		{Username: "tok-user"},
		{Password: "tok-pass"},
		{Hash: "tokhash"},
		{Salt: "toksalt"},
		{IP: "tok.ip"},
		{Phone: "tokphone"},
		{Name: "tok-name"},
		{Database: "tokdb"},
		{URL: "tok-url"},
		{Email: "unrelated@x.com"},
	}
	for _, r := range rows {
		if err := db.Insert(r); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	type tcase struct {
		name     string
		scanType sources.ScanType
		target   string
		wantHit  bool
	}

	cases := []tcase{
		// email: email + username only
		{"email→email", sources.TypeEmail, "tok-email", true},
		{"email→username", sources.TypeEmail, "tok-user", true},
		{"email→phone", sources.TypeEmail, "tokphone", false},

		// username: username, email, phone
		{"username→username", sources.TypeUsername, "tok-user", true},
		{"username→email", sources.TypeUsername, "tok-email", true},
		{"username→phone", sources.TypeUsername, "tokphone", true},
		{"username→password", sources.TypeUsername, "tok-pass", false},

		// phone: phone, username
		{"phone→phone", sources.TypePhone, "tokphone", true},
		{"phone→username", sources.TypePhone, "tok-user", true},
		{"phone→email", sources.TypePhone, "tok-email", false},

		// domain: all columns
		{"domain→email", sources.TypeDomain, "tok-email", true},
		{"domain→database", sources.TypeDomain, "tokdb", true},
		{"domain→url", sources.TypeDomain, "tok-url", true},
		{"domain→hash", sources.TypeDomain, "tokhash", true},

		// keyword: all columns
		{"keyword→ip", sources.TypeKeyword, "tok.ip", true},
		{"keyword→salt", sources.TypeKeyword, "toksalt", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			results := collectSearch(t, db, c.target, c.scanType)
			hit := len(results) > 0
			if hit != c.wantHit {
				t.Errorf("search %q in %v: wantHit=%v got %d results", c.target, c.scanType, c.wantHit, len(results))
			}
		})
	}
}

func TestLeakerDB_Search_CaseInsensitive(t *testing.T) {
	path := tempDBPath(t)
	db, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Insert(&sources.Result{Email: "Foo@X.COM"}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	for _, target := range []string{"foo@x.com", "FOO", "oo@x"} {
		results := collectSearch(t, db, target, sources.TypeEmail)
		if len(results) != 1 {
			t.Errorf("target %q: expected 1 result, got %d", target, len(results))
		}
	}
}

func TestLeakerDB_Search_OverwritesSourceToLocal(t *testing.T) {
	path := tempDBPath(t)
	db, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Insert(&sources.Result{Source: "snusbase", Email: "a@b.com"}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	results := collectSearch(t, db, "a@b.com", sources.TypeEmail)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Source != sources.LocalSourceName {
		t.Errorf("returned Source should be %q, got %q", sources.LocalSourceName, results[0].Source)
	}

	// The DB row still preserves the original source
	var storedSource string
	if err := db.db.QueryRow("SELECT source FROM leaks WHERE email='a@b.com'").Scan(&storedSource); err != nil {
		t.Fatalf("query stored source: %v", err)
	}
	if storedSource != "snusbase" {
		t.Errorf("stored source should be %q, got %q", "snusbase", storedSource)
	}
}

func TestLeakerDB_Search_PreFilledChecksum(t *testing.T) {
	path := tempDBPath(t)
	db, err := OpenLeakerDB(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	original := &sources.Result{Email: "foo@bar.com", Password: "p"}
	expected := original.Checksum()
	if err := db.Insert(original); err != nil {
		t.Fatalf("insert: %v", err)
	}

	results := collectSearch(t, db, "foo@bar.com", sources.TypeEmail)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if got := results[0].Checksum(); got != expected {
		t.Errorf("checksum mismatch: want %q, got %q", expected, got)
	}
}

// collectSearch drains the search channel into a slice for easy assertion.
func collectSearch(t *testing.T, db *LeakerDB, target string, st sources.ScanType) []sources.Result {
	t.Helper()
	var out []sources.Result
	for r := range db.Search(context.Background(), target, st) {
		if r.Error != nil {
			t.Fatalf("search error: %v", r.Error)
		}
		out = append(out, r)
	}
	return out
}
