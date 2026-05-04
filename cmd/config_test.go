package cmd

import "testing"

func TestResolveDBPathFlagWinsOverEnv(t *testing.T) {
	got := resolveDBPath("/tmp/flag.db", func(key string) string {
		if key == "LEAKER_DB" {
			return "/tmp/env.db"
		}
		return ""
	})
	if got != "/tmp/flag.db" {
		t.Fatalf("expected flag DB path to win, got %q", got)
	}
}

func TestResolveDBPathFallsBackToEnv(t *testing.T) {
	got := resolveDBPath("", func(key string) string {
		if key == "LEAKER_DB" {
			return "/tmp/env.db"
		}
		return ""
	})
	if got != "/tmp/env.db" {
		t.Fatalf("expected env DB path, got %q", got)
	}
}

func TestResolveNoWriteDBFlagWinsOverEnv(t *testing.T) {
	warnings := 0
	got := resolveNoWriteDB(true, func(key string) string {
		if key == "LEAKER_NO_WRITE_DB" {
			return "false"
		}
		return ""
	}, func(format string, args ...any) { warnings++ })
	if !got {
		t.Fatalf("expected --no-write-db flag to force true")
	}
	if warnings != 0 {
		t.Fatalf("expected no warnings, got %d", warnings)
	}
}

func TestResolveNoWriteDBParsesEnv(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "true", value: "true", want: true},
		{name: "one", value: "1", want: true},
		{name: "false", value: "false", want: false},
		{name: "zero", value: "0", want: false},
		{name: "trimmed true", value: " TRUE ", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveNoWriteDB(false, func(key string) string {
				if key == "LEAKER_NO_WRITE_DB" {
					return tc.value
				}
				return ""
			}, func(format string, args ...any) { t.Fatalf("unexpected warning for %q", tc.value) })
			if got != tc.want {
				t.Fatalf("expected %v for %q, got %v", tc.want, tc.value, got)
			}
		})
	}
}

func TestResolveNoWriteDBInvalidEnvWarnsAndFallsBackFalse(t *testing.T) {
	warnings := 0
	got := resolveNoWriteDB(false, func(key string) string {
		if key == "LEAKER_NO_WRITE_DB" {
			return "definitely"
		}
		return ""
	}, func(format string, args ...any) { warnings++ })
	if got {
		t.Fatalf("expected invalid env value to fall back false")
	}
	if warnings != 1 {
		t.Fatalf("expected one warning, got %d", warnings)
	}
}
