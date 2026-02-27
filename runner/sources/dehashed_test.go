package sources

import (
	"context"
	"net/http"
	"testing"
)

func TestDeHashedName(t *testing.T) {
	s := &DeHashed{}
	if s.Name() != "dehashed" {
		t.Errorf("expected dehashed, got %s", s.Name())
	}
}

func TestDeHashedNeedsKey(t *testing.T) {
	s := &DeHashed{}
	if !s.NeedsKey() {
		t.Error("expected NeedsKey to be true")
	}
}

func TestDeHashedRunNoKey(t *testing.T) {
	s := &DeHashed{}
	ch := s.Run(context.Background(), "test@example.com", TypeEmail, &Session{Client: http.DefaultClient})
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 results with no API key, got %d", count)
	}
}

func TestDeHashedRateLimit(t *testing.T) {
	s := &DeHashed{}
	if s.RateLimit() != 10 {
		t.Errorf("expected rate limit 10, got %d", s.RateLimit())
	}
}
