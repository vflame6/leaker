package sources

import (
	"context"
	"net/http"
	"testing"
)

func TestLeakLookupName(t *testing.T) {
	s := &LeakLookup{}
	if s.Name() != "leaklookup" {
		t.Errorf("expected leaklookup, got %s", s.Name())
	}
}

func TestLeakLookupNeedsKey(t *testing.T) {
	s := &LeakLookup{}
	if !s.NeedsKey() {
		t.Error("expected NeedsKey to be true")
	}
}

func TestLeakLookupRunNoKey(t *testing.T) {
	s := &LeakLookup{}
	ch := s.Run(context.Background(), "test@example.com", TypeEmail, &Session{Client: http.DefaultClient})
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 results with no API key, got %d", count)
	}
}

func TestLeakLookupRateLimit(t *testing.T) {
	s := &LeakLookup{}
	if s.RateLimit() != 1 {
		t.Errorf("expected rate limit 1, got %d", s.RateLimit())
	}
}
