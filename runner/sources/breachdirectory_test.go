package sources

import (
	"context"
	"net/http"
	"testing"
)

func TestBreachDirectoryName(t *testing.T) {
	s := &BreachDirectory{}
	if s.Name() != "breachdirectory" {
		t.Errorf("expected breachdirectory, got %s", s.Name())
	}
}

func TestBreachDirectoryNeedsKey(t *testing.T) {
	s := &BreachDirectory{}
	if !s.NeedsKey() {
		t.Error("expected NeedsKey to be true")
	}
}

func TestBreachDirectoryIsDefault(t *testing.T) {
	s := &BreachDirectory{}
	if s.IsDefault() {
		t.Error("expected IsDefault to be false")
	}
}

func TestBreachDirectoryRunNoKey(t *testing.T) {
	s := &BreachDirectory{}
	ch := s.Run(context.Background(), "test@example.com", TypeEmail, &Session{Client: http.DefaultClient})
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 results with no API key, got %d", count)
	}
}

func TestBreachDirectoryRateLimit(t *testing.T) {
	s := &BreachDirectory{}
	if s.RateLimit() != 1 {
		t.Errorf("expected rate limit 1, got %d", s.RateLimit())
	}
}
