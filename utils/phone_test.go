package utils

import "testing"

func TestExtractPhoneDigits(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"+1 (555) 234 10 96", "15552341096"},
		{"+998-50-123-45-67", "998501234567"},
		{"15552341096", "15552341096"},
		{"+1-555-234-10-96", "15552341096"},
		{"(495) 123-4567", "4951234567"},
		{"+1 555 0123", "15550123"},
		{"", ""},
		{"no digits here!", ""},
		{"  +7 999 111 22 33  ", "79991112233"},
	}

	for _, tt := range tests {
		got := ExtractPhoneDigits(tt.input)
		if got != tt.expected {
			t.Errorf("ExtractPhoneDigits(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
