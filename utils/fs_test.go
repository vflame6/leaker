package utils

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestFileExists(t *testing.T) {
	dir := t.TempDir()

	existing := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(existing, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	if !FileExists(existing) {
		t.Errorf("expected FileExists(%q) = true", existing)
	}
	if FileExists(filepath.Join(dir, "missing.txt")) {
		t.Error("expected FileExists for missing file = false")
	}
	// directory should not count as a file
	if FileExists(dir) {
		t.Error("expected FileExists for directory = false")
	}
}

func TestCreateOutputFile_CreatesNewFileAndParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "dir", "out.txt")
	f, err := CreateOutputFile(path, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = f.Close()
	if !FileExists(path) {
		t.Error("expected the file to exist")
	}
}

func TestCreateOutputFile_RefusesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateOutputFile(path, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "keep\n" {
		t.Errorf("existing file changed: %q", data)
	}
}

// TestCreateOutputFile_OverwriteTruncates: --overwrite replaces the old
// content instead of appending to it.
func TestCreateOutputFile_OverwriteTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("old line 1\nold line 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := CreateOutputFile(path, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = f.WriteString("new\n")
	_ = f.Close()
	if data, _ := os.ReadFile(path); string(data) != "new\n" {
		t.Errorf("content = %q, want only the new line", data)
	}
}

func TestCreateOutputFile_EmptyFilename(t *testing.T) {
	if _, err := CreateOutputFile("", false); err == nil {
		t.Error("expected error for empty filename")
	}
}
