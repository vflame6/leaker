// Package runner: db.go implements the LeakerDB module — a wrapper around
// a local SQLite database used to cache leak results across runs, so
// repeating the same search does not re-hit the online APIs.
package runner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// leaksDDL is the canonical CREATE TABLE statement for the leaks table.
// Its sha256 is written to leaker_meta.schema_hash at creation time and
// compared on every subsequent open; any change to this string produces
// a new hash and causes existing DBs to be rejected with an incompatible
// schema error.
const leaksDDL = `CREATE TABLE leaks (
    checksum   TEXT PRIMARY KEY NOT NULL,
    source     TEXT NOT NULL,
    email      TEXT NOT NULL DEFAULT '',
    username   TEXT NOT NULL DEFAULT '',
    password   TEXT NOT NULL DEFAULT '',
    hash       TEXT NOT NULL DEFAULT '',
    salt       TEXT NOT NULL DEFAULT '',
    ip         TEXT NOT NULL DEFAULT '',
    phone      TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL DEFAULT '',
    database   TEXT NOT NULL DEFAULT '',
    url        TEXT NOT NULL DEFAULT '',
    extra      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
)`

const metaDDL = `CREATE TABLE leaker_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
)`

const schemaVersion = "1"

// leaksIndexDDLs creates partial indexes on the most-searched columns,
// restricted to non-empty values to keep them compact.
var leaksIndexDDLs = []string{
	`CREATE INDEX idx_leaks_email    ON leaks(email)    WHERE email    != ''`,
	`CREATE INDEX idx_leaks_username ON leaks(username) WHERE username != ''`,
	`CREATE INDEX idx_leaks_phone    ON leaks(phone)    WHERE phone    != ''`,
}

// allLeakColumns is the ordered list of data columns on the leaks table,
// excluding the primary-key checksum and the non-content metadata columns.
// Used by Search when the scan type maps to "every column" (domain, keyword).
var allLeakColumns = []string{
	"email", "username", "password", "hash", "salt",
	"ip", "phone", "name", "database", "url",
}

// insertSQL is the prepared insert statement. INSERT OR IGNORE enforces
// dedup via the primary-key constraint on checksum — duplicates are
// silently dropped without surfacing as errors.
const insertSQL = `INSERT OR IGNORE INTO leaks (
    checksum, source, email, username, password, hash, salt, ip, phone,
    name, database, url, extra, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// expectedSchemaHash returns the sha256 hex of the canonical leaks DDL.
// The leaker_meta table is the validator itself and is not part of the hash.
func expectedSchemaHash() string {
	sum := sha256.Sum256([]byte(leaksDDL))
	return hex.EncodeToString(sum[:])
}

// LeakerDB wraps a SQLite handle plus prepared statements for inserting
// and searching cached leak results. Constructed once per runner, closed
// when the runner exits. Safe for use from the consumer goroutine only;
// it is not intended for concurrent writers from the same process.
type LeakerDB struct {
	db         *sql.DB
	path       string
	writable   bool
	insertStmt *sql.Stmt
}

// OpenLeakerDB opens (or creates) the SQLite database at path, verifies
// its schema, and returns a usable handle.
//
//   - If path does not exist AND writable is true, the file (and any
//     missing parent directories) is created and the schema is bootstrapped.
//   - If path does not exist AND writable is false, a nil *LeakerDB is
//     returned without error; callers should treat this as "no DB
//     available" (reads return empty, writes are no-ops).
//   - If path exists with an incompatible schema, a descriptive error is
//     returned. The caller (runner.NewRunner) turns this into a fatal.
func OpenLeakerDB(path string, writable bool) (*LeakerDB, error) {
	// Path resolution
	if path == "" {
		return nil, errors.New("leaker DB path is empty")
	}

	_, statErr := os.Stat(path)
	exists := statErr == nil

	if !exists {
		if !writable {
			// Read-only mode and no DB: nothing to do. Return nil;
			// callers treat this as "DB unavailable".
			return nil, nil
		}
		// Create parent directory if needed.
		if dir := filepath.Dir(path); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create DB parent dir: %w", err)
			}
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite at %s: %w", path, err)
	}
	// Single connection is fine for our write patterns; more just means
	// more file handles with nothing to gain.
	db.SetMaxOpenConns(1)

	l := &LeakerDB{db: db, path: path, writable: writable}

	if !exists {
		if err := bootstrapSchema(db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("bootstrap schema: %w", err)
		}
	} else {
		if err := verifySchema(db); err != nil {
			_ = db.Close()
			return nil, err
		}
	}

	if writable {
		stmt, err := db.Prepare(insertSQL)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("prepare insert: %w", err)
		}
		l.insertStmt = stmt
	}

	return l, nil
}

// bootstrapSchema creates both tables, the partial indexes, and seeds
// leaker_meta. Called exactly once per fresh DB.
func bootstrapSchema(db *sql.DB) error {
	if _, err := db.Exec(metaDDL); err != nil {
		return fmt.Errorf("create leaker_meta: %w", err)
	}
	if _, err := db.Exec(leaksDDL); err != nil {
		return fmt.Errorf("create leaks: %w", err)
	}
	for _, idx := range leaksIndexDDLs {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	if _, err := db.Exec(
		"INSERT INTO leaker_meta (key, value) VALUES (?, ?), (?, ?)",
		"schema_version", schemaVersion,
		"schema_hash", expectedSchemaHash(),
	); err != nil {
		return fmt.Errorf("seed leaker_meta: %w", err)
	}
	return nil
}

// verifySchema compares the stored schema_hash against the expected value.
// Missing table or missing/mismatched hash returns a descriptive error.
func verifySchema(db *sql.DB) error {
	// Does leaker_meta exist?
	var name string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='leaker_meta'",
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("leaker_meta table missing — this does not appear to be a leaker database. Back it up and remove it to continue")
	}
	if err != nil {
		return fmt.Errorf("query sqlite_master: %w", err)
	}

	var stored string
	err = db.QueryRow("SELECT value FROM leaker_meta WHERE key='schema_hash'").Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("leaker_meta.schema_hash row missing — incompatible schema. Back up and remove the DB to continue")
	}
	if err != nil {
		return fmt.Errorf("query schema_hash: %w", err)
	}
	if stored != expectedSchemaHash() {
		return errors.New("existing leaker DB uses an incompatible schema. Back up and remove it to continue")
	}
	return nil
}

// Close closes the prepared statements and the underlying database handle.
// Safe to call on a nil receiver.
func (l *LeakerDB) Close() error {
	if l == nil {
		return nil
	}
	if l.insertStmt != nil {
		_ = l.insertStmt.Close()
	}
	if l.db != nil {
		return l.db.Close()
	}
	return nil
}

// Insert writes a single result to the cache. Duplicates (same checksum)
// are silently ignored via the INSERT OR IGNORE clause. A no-op on read-only
// or nil handles.
func (l *LeakerDB) Insert(r *sources.Result) error {
	if l == nil || !l.writable || l.insertStmt == nil {
		return nil
	}
	if r == nil {
		return nil
	}

	extraJSON, err := encodeExtra(r.Extra)
	if err != nil {
		return fmt.Errorf("encode extra: %w", err)
	}

	_, err = l.insertStmt.Exec(
		r.Checksum(),
		r.Source,
		r.Email,
		r.Username,
		r.Password,
		r.Hash,
		r.Salt,
		r.IP,
		r.Phone,
		r.Name,
		r.Database,
		r.URL,
		extraJSON,
		time.Now().Unix(),
	)
	return err
}

// Search performs a case-insensitive LIKE %target% query against the columns
// appropriate for the given scan type and streams matching results back on
// a channel. The channel is closed after the last row (or immediately if the
// DB is nil). Returned Results have Source overwritten to LocalSourceName
// so users see [local] in verbose output; the original source name is still
// stored in the row's `source` column for anyone who wants to investigate
// provenance by opening the DB file directly.
func (l *LeakerDB) Search(ctx context.Context, target string, scanType sources.ScanType) <-chan sources.Result {
	out := make(chan sources.Result)

	go func() {
		defer close(out)
		if l == nil || l.db == nil {
			return
		}

		cols := searchColumnsFor(scanType)
		if len(cols) == 0 {
			return
		}

		query, args := buildSearchQuery(cols, target)

		rows, err := l.db.QueryContext(ctx, query, args...)
		if err != nil {
			select {
			case out <- sources.Result{Source: sources.LocalSourceName, Error: fmt.Errorf("local search: %w", err)}:
			case <-ctx.Done():
			}
			return
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var (
				checksum  string
				_origSrc  string
				email     string
				username  string
				password  string
				hashField string
				salt      string
				ip        string
				phone     string
				name      string
				database  string
				url       string
				extraJSON string
			)
			if err := rows.Scan(
				&checksum, &_origSrc, &email, &username, &password,
				&hashField, &salt, &ip, &phone, &name, &database, &url, &extraJSON,
			); err != nil {
				logger.Errorf("local DB row scan: %s", err)
				continue
			}

			extra, err := decodeExtra(extraJSON)
			if err != nil {
				logger.Errorf("local DB extra decode: %s", err)
				// continue with nil extra rather than skipping the row
			}

			r := sources.Result{
				Source:   sources.LocalSourceName, // override: user sees [local]
				Email:    email,
				Username: username,
				Password: password,
				Hash:     hashField,
				Salt:     salt,
				IP:       ip,
				Phone:    phone,
				Name:     name,
				Database: database,
				URL:      url,
				Extra:    extra,
			}
			r.SetCachedChecksum(checksum)

			select {
			case out <- r:
			case <-ctx.Done():
				return
			}
		}
		if err := rows.Err(); err != nil {
			select {
			case out <- sources.Result{Source: sources.LocalSourceName, Error: fmt.Errorf("local search rows: %w", err)}:
			case <-ctx.Done():
			}
		}
	}()

	return out
}

// searchColumnsFor maps a scan type to the set of columns the LIKE query
// should touch. See spec §5 "Search column mapping".
func searchColumnsFor(scanType sources.ScanType) []string {
	switch scanType {
	case sources.TypeEmail:
		return []string{"email", "username"}
	case sources.TypeUsername:
		return []string{"username", "email", "phone"}
	case sources.TypePhone:
		return []string{"phone", "username"}
	case sources.TypeDomain, sources.TypeKeyword:
		return allLeakColumns
	}
	return nil
}

// buildSearchQuery assembles the SELECT + WHERE clause for a given set of
// columns and target. Uses LOWER(col) LIKE LOWER(?) for explicit
// unicode-aware case-insensitive matching; the target is wrapped as
// %target% once and bound once per column.
func buildSearchQuery(cols []string, target string) (string, []any) {
	query := `SELECT checksum, source, email, username, password, hash, salt, ip, phone, name, database, url, extra
  FROM leaks
 WHERE `
	pattern := "%" + target + "%"
	args := make([]any, 0, len(cols))
	for i, col := range cols {
		if i > 0 {
			query += " OR "
		}
		query += "LOWER(" + col + ") LIKE LOWER(?)"
		args = append(args, pattern)
	}
	return query, args
}

// encodeExtra serializes Result.Extra as JSON with sorted keys so the
// encoding is deterministic (and therefore the checksum stable).
func encodeExtra(extra map[string]string) (string, error) {
	if len(extra) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build an ordered slice of pairs and marshal manually so json doesn't
	// reorder keys on us (json.Marshal of a map already sorts, but being
	// explicit here protects us if that ever changes).
	type kv struct {
		K string `json:"k"`
		V string `json:"v"`
	}
	pairs := make([]kv, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, kv{K: k, V: extra[k]})
	}
	b, err := json.Marshal(pairs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeExtra is the inverse of encodeExtra.
func decodeExtra(s string) (map[string]string, error) {
	if s == "" {
		return nil, nil
	}
	type kv struct {
		K string `json:"k"`
		V string `json:"v"`
	}
	var pairs []kv
	if err := json.Unmarshal([]byte(s), &pairs); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p.K] = p.V
	}
	return out, nil
}
