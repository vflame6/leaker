package sources

import (
	"context"
	"net/http"
	"testing"
)

func TestIntelXName(t *testing.T) {
	s := &IntelX{}
	if s.Name() != "intelx" {
		t.Errorf("expected intelx, got %s", s.Name())
	}
}

func TestIntelXNeedsKey(t *testing.T) {
	s := &IntelX{}
	if !s.NeedsKey() {
		t.Error("expected NeedsKey to be true")
	}
}

func TestIntelXIsDefault(t *testing.T) {
	s := &IntelX{}
	if s.IsDefault() {
		t.Error("expected IsDefault to be false")
	}
}

func TestIntelXRunNoKey(t *testing.T) {
	s := &IntelX{}
	ch := s.Run(context.Background(), "test@example.com", TypeEmail, &Session{Client: http.DefaultClient})
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 results with no API key, got %d", count)
	}
}

func TestIntelXAddApiKeys(t *testing.T) {
	s := &IntelX{}
	s.AddApiKeys([]string{"2.intelx.io:uuid-key-1", "free.intelx.io:uuid-key-2"})
	if len(s.apiKeys) != 2 {
		t.Errorf("expected 2 API keys, got %d", len(s.apiKeys))
	}
	if s.apiKeys[0].host != "2.intelx.io" || s.apiKeys[0].apiKey != "uuid-key-1" {
		t.Errorf("unexpected key[0]: %+v", s.apiKeys[0])
	}
	if s.apiKeys[1].host != "free.intelx.io" || s.apiKeys[1].apiKey != "uuid-key-2" {
		t.Errorf("unexpected key[1]: %+v", s.apiKeys[1])
	}
}

func TestIntelXAddApiKeysInvalidFormat(t *testing.T) {
	s := &IntelX{}
	s.AddApiKeys([]string{"invalid-no-colon"})
	if len(s.apiKeys) != 0 {
		t.Errorf("expected 0 API keys for invalid format, got %d", len(s.apiKeys))
	}
}

func TestIntelXRateLimit(t *testing.T) {
	s := &IntelX{}
	if s.RateLimit() != 1 {
		t.Errorf("expected rate limit 1, got %d", s.RateLimit())
	}
}

func TestIntelXFormatRecord(t *testing.T) {
	s := &IntelX{}
	record := intelxResultRecord{
		Name:   "test@example.com",
		MediaH: "Paste",
		Bucket: "pastes",
		Date:   "2024-01-01",
	}
	result := s.formatRecord(record)
	expected := "name:test@example.com, type:Paste, bucket:pastes, date:2024-01-01"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}
