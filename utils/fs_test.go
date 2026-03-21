package utils

import (
	"os"
	"path/filepath"
	"strings"
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

func TestParseTargets_SingleTarget(t *testing.T) {
	r, err := ParseTargets("user@example.com", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	buf := new(strings.Builder)
	tmp := make([]byte, 64)
	n, _ := r.Read(tmp)
	buf.Write(tmp[:n])
	if !strings.Contains(buf.String(), "user@example.com") {
		t.Errorf("expected email in reader, got: %q", buf.String())
	}
}

func TestParseTargets_FileTarget(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "targets.txt")
	if err := os.WriteFile(f, []byte("a@example.com\nb@example.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	r, err := ParseTargets(f, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil reader")
	}
}

func TestParseTargets_Empty(t *testing.T) {
	_, err := ParseTargets("", false)
	if err == nil {
		t.Error("expected error for empty targets with stdin=false")
	}
}

func TestParseTargets_StdinAndFile(t *testing.T) {
	// Simulate: echo "stdin@example.com" | leaker email targets.txt
	// When both stdin=true and a file target are given, both should be read.
	// We can't easily mock os.Stdin here, so we test that a file target
	// is returned even when stdin=true (MultiReader behavior).
	dir := t.TempDir()
	f := filepath.Join(dir, "targets.txt")
	if err := os.WriteFile(f, []byte("file@example.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// stdin=true but os.Stdin is a terminal in test — MultiReader will include
	// both os.Stdin and the file reader. We verify no error is returned.
	r, err := ParseTargets(f, true)
	if err != nil {
		t.Fatalf("unexpected error with stdin+file: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil reader for stdin+file")
	}
}

func TestParseTargets_StdinAndInline(t *testing.T) {
	// Simulate: echo "stdin@example.com" | leaker email "inline@example.com"
	r, err := ParseTargets("inline@example.com", true)
	if err != nil {
		t.Fatalf("unexpected error with stdin+inline: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil reader for stdin+inline")
	}
}

func TestCreateFileWithSafe_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	f, err := CreateFileWithSafe(path, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if !FileExists(path) {
		t.Error("expected file to exist after CreateFileWithSafe")
	}
}

func TestCreateFileWithSafe_ErrorIfExistsNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(path, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := CreateFileWithSafe(path, false, false)
	if err == nil {
		t.Error("expected error when file exists and overwrite=false")
	}
}

func TestCreateFileWithSafe_OverwriteAllowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := CreateFileWithSafe(path, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestCreateFileWithSafe_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "out.txt")

	f, err := CreateFileWithSafe(path, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if !FileExists(path) {
		t.Error("expected nested file to exist")
	}
}

func TestCreateFileWithSafe_EmptyFilename(t *testing.T) {
	_, err := CreateFileWithSafe("", false, false)
	if err == nil {
		t.Error("expected error for empty filename")
	}
}
