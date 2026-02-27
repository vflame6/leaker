package sources

import (
	"context"
	"net/http"
	"testing"
)

func TestSnusbaseName(t *testing.T) {
	s := &Snusbase{}
	if s.Name() != "snusbase" {
		t.Errorf("expected snusbase, got %s", s.Name())
	}
}

func TestSnusbaseNeedsKey(t *testing.T) {
	s := &Snusbase{}
	if !s.NeedsKey() {
		t.Error("expected NeedsKey to be true")
	}
}

func TestSnusbaseRunNoKey(t *testing.T) {
	s := &Snusbase{}
	ch := s.Run(context.Background(), "test@example.com", TypeEmail, &Session{Client: http.DefaultClient})
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 results with no API key, got %d", count)
	}
}

func TestSnusbaseRateLimit(t *testing.T) {
	s := &Snusbase{}
	if s.RateLimit() != 2 {
		t.Errorf("expected rate limit 2, got %d", s.RateLimit())
	}
}
