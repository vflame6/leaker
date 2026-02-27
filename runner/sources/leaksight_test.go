package sources

import (
	"context"
	"net/http"
	"testing"
)

func TestLeakSightName(t *testing.T) {
	s := &LeakSight{}
	if s.Name() != "leaksight" {
		t.Errorf("expected leaksight, got %s", s.Name())
	}
}

func TestLeakSightNeedsKey(t *testing.T) {
	s := &LeakSight{}
	if !s.NeedsKey() {
		t.Error("expected NeedsKey to be true")
	}
}

func TestLeakSightRunNoKey(t *testing.T) {
	s := &LeakSight{}
	ch := s.Run(context.Background(), "test@example.com", TypeEmail, &Session{Client: http.DefaultClient})
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 results with no API key, got %d", count)
	}
}

func TestLeakSightRateLimit(t *testing.T) {
	s := &LeakSight{}
	if s.RateLimit() != 2 {
		t.Errorf("expected rate limit 2, got %d", s.RateLimit())
	}
}
