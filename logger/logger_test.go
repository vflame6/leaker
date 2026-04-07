package logger

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestSetAndGetMaxLevel(t *testing.T) {
	l := New(LevelInfo, &bytes.Buffer{})
	if l.GetMaxLevel() != LevelInfo {
		t.Fatalf("expected LevelInfo, got %v", l.GetMaxLevel())
	}
	l.SetMaxLevel(LevelDebug)
	if l.GetMaxLevel() != LevelDebug {
		t.Fatalf("expected LevelDebug, got %v", l.GetMaxLevel())
	}
}

func TestMessagesAboveMaxLevelSuppressed(t *testing.T) {
	var buf bytes.Buffer
	l := New(LevelWarning, &buf)

	l.Debug("should not appear")
	l.Info("should not appear either")

	if buf.Len() != 0 {
		t.Fatalf("expected no output, got: %q", buf.String())
	}
}

func TestMessagesAtOrBelowMaxLevelWritten(t *testing.T) {
	var buf bytes.Buffer
	l := New(LevelInfo, &buf)

	l.Info("hello")
	l.Warn("world")
	l.Error("oops")

	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in output, got: %q", out)
	}
	if !strings.Contains(out, "world") {
		t.Errorf("expected 'world' in output, got: %q", out)
	}
	if !strings.Contains(out, "oops") {
		t.Errorf("expected 'oops' in output, got: %q", out)
	}
}

func TestLevelPrefixesInOutput(t *testing.T) {
	tests := []struct {
		name     string
		logFunc  func(l *Logger, msg string)
		expected string
	}{
		{"error", func(l *Logger, msg string) { l.Errorf("%s", msg) }, "[ERR]"},
		{"warn", func(l *Logger, msg string) { l.Warnf("%s", msg) }, "[WARN]"},
		{"info", func(l *Logger, msg string) { l.Infof("%s", msg) }, "[INFO]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(LevelVerbose, &buf)
			l.SetNoColor(true)
			tt.logFunc(l, "testmsg")
			if !strings.Contains(buf.String(), tt.expected) {
				t.Errorf("expected prefix %q in output %q", tt.expected, buf.String())
			}
		})
	}
}

func TestConcurrentWritesSafe(t *testing.T) {
	var buf bytes.Buffer
	l := New(LevelInfo, &buf)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Info("concurrent message")
		}()
	}
	wg.Wait()

	// if we get here without a race detector hit, we're fine
	if buf.Len() == 0 {
		t.Error("expected some output from concurrent writes")
	}
}

func TestDebugSuppressedAtInfoLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(LevelInfo, &buf)
	l.Debug("should not appear")
	l.Debugf("also %s", "hidden")
	if buf.Len() != 0 {
		t.Errorf("debug messages should be suppressed at LevelInfo, got: %q", buf.String())
	}
}
