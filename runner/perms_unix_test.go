//go:build !windows

package runner

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/vflame6/leaker/runner/sources"
	"github.com/vflame6/leaker/utils"
)

func expectMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Errorf("stat %s: %v", path, err)
		return
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s has mode %04o, want %04o", filepath.Base(path), got, want)
	}
}

// TestPrivatePermissions_Umask022: everything leaker creates is private to
// the user, even with the common umask 022.
func TestPrivatePermissions_Umask022(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	root := t.TempDir()

	configDir := filepath.Join(root, "config")
	config := filepath.Join(configDir, "provider-config.yaml")
	if err := createProviderConfigYAML(config); err != nil {
		t.Fatal(err)
	}
	expectMode(t, configDir, 0o700)
	expectMode(t, config, 0o600)

	dbDir := filepath.Join(root, "db")
	dbPath := filepath.Join(dbDir, "leaker.db")
	db, err := OpenLeakerDB(dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Insert(&sources.Result{Email: "jane@example.com", Password: "hunter2"}); err != nil {
		t.Fatal(err)
	}
	expectMode(t, dbDir, 0o700)
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		expectMode(t, p, 0o600)
	}

	outDir := filepath.Join(root, "out")
	f, err := utils.CreateOutputFile(filepath.Join(outDir, "results.txt"), false)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	expectMode(t, outDir, 0o700)
	expectMode(t, filepath.Join(outDir, "results.txt"), 0o600)
}

// TestNewRunner_RestrictsReadableConfigAndDB: files left readable by older
// versions are fixed with a warning.
func TestNewRunner_RestrictsReadableConfigAndDB(t *testing.T) {
	logs := captureLogs(t)
	dir := t.TempDir()
	config := filepath.Join(dir, "provider-config.yaml")
	if err := os.WriteFile(config, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "leaker.db")
	db, err := OpenLeakerDB(dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	for _, p := range []string{config, dbPath} {
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r, err := NewRunner(&Options{ProviderConfig: config, DBPath: dbPath, Sources: []string{"proxynova"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	expectMode(t, config, 0o600)
	expectMode(t, dbPath, 0o600)
	for _, p := range []string{config, dbPath} {
		warned := false
		for _, line := range strings.Split(logs.String(), "\n") {
			if strings.Contains(line, p) && strings.Contains(line, "other users") {
				warned = true
			}
		}
		if !warned {
			t.Errorf("expected a warning about other users naming %s, logs:\n%s", p, logs)
		}
	}
}

// TestEnumerate_FirstDBWriteNotice: the first stored result tells the user
// where results go and how to turn that off, once per run.
func TestEnumerate_FirstDBWriteNotice(t *testing.T) {
	logs := captureLogs(t)
	dbPath := filepath.Join(t.TempDir(), "leaker.db")
	db, err := OpenLeakerDB(dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.NoFilter = true
	r.leakerDB = db
	r.scanSources = []sources.Source{&fakeSource{name: "s", emits: []sources.Result{
		{Source: "s", Email: "a@example.com", Password: "1"}, {Source: "s", Email: "a@example.com", Password: "2"},
	}}}
	for _, target := range []string{"a@example.com", "a@example.com"} {
		if err := r.EnumerateSingleTarget(context.Background(), target, sources.TypeEmail, []io.Writer{&bytes.Buffer{}}); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(logs.String(), "Saving results to the local database"); n != 1 {
		t.Errorf("notice printed %d times, want once:\n%s", n, logs)
	}
	if !strings.Contains(logs.String(), dbPath) {
		t.Errorf("notice must name the database path:\n%s", logs)
	}
}
