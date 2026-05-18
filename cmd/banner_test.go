package cmd

import (
	"fmt"
	"strings"
	"testing"
)

func TestBannerIncludesVersionAndAuthor(t *testing.T) {
	banner := fmt.Sprintf(BANNER, "v9.9.9-test", AUTHOR)

	if !strings.Contains(banner, "v9.9.9-test") {
		t.Fatalf("expected banner to include version, got %q", banner)
	}
	if !strings.Contains(banner, AUTHOR) {
		t.Fatalf("expected banner to include author, got %q", banner)
	}
	if !strings.Contains(banner, "Made by") {
		t.Fatalf("expected banner attribution line, got %q", banner)
	}
}

func TestDefaultVersionIsNonEmpty(t *testing.T) {
	if strings.TrimSpace(VERSION) == "" {
		t.Fatal("expected VERSION to be non-empty")
	}
}
