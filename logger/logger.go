// Package logger provides a simple leveled logging implementation for CLI tools.
package logger

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Level represents the severity of a log message.
type Level int

const (
	LevelFatal Level = iota
	LevelError
	LevelWarning
	LevelInfo
	LevelDebug
	LevelVerbose
)

var levelPrefixes = map[Level]string{
	LevelFatal:   "[FATAL]",
	LevelError:   "[ERR]",
	LevelWarning: "[WARN]",
	LevelInfo:    "[INFO]",
	LevelDebug:   "[DEBUG]",
	LevelVerbose: "[VERB]",
}

// String returns the string representation of a Level.
func (l Level) String() string {
	if prefix, ok := levelPrefixes[l]; ok {
		return prefix
	}
	return "[UNKNOWN]"
}

// Logger is a leveled logger that outputs messages with level prefixes.
type Logger struct {
	mu       sync.RWMutex
	maxLevel Level
	output   io.Writer
}

// New creates a new Logger with the specified max level and output writer.
func New(maxLevel Level, output io.Writer) *Logger {
	return &Logger{
		maxLevel: maxLevel,
		output:   output,
	}
}

// SetMaxLevel sets the maximum logging level.
// Messages with a level higher than maxLevel will not be logged.
func (l *Logger) SetMaxLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maxLevel = level
}

// GetMaxLevel returns the current maximum logging level.
func (l *Logger) GetMaxLevel() Level {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.maxLevel
}

// SetOutput sets the output destination for the logger.
func (l *Logger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output = w
}

func (l *Logger) log(level Level, format string, args ...any) {
	l.mu.RLock()
	maxLevel := l.maxLevel
	output := l.output
	l.mu.RUnlock()

	if level > maxLevel {
		return
	}

	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(output, "%s %s\n", level.String(), msg)
}

// Fatal logs a message at Fatal level and exits the program.
func (l *Logger) Fatal(args ...any) {
	l.log(LevelFatal, "%s", fmt.Sprint(args...))
	os.Exit(1)
}

// Fatalf logs a formatted message at Fatal level and exits the program.
func (l *Logger) Fatalf(format string, args ...any) {
	l.log(LevelFatal, format, args...)
	os.Exit(1)
}

// Error logs a message at Error level.
func (l *Logger) Error(args ...any) {
	l.log(LevelError, "%s", fmt.Sprint(args...))
}

// Errorf logs a formatted message at Error level.
func (l *Logger) Errorf(format string, args ...any) {
	l.log(LevelError, format, args...)
}

// Warn logs a message at Warning level.
func (l *Logger) Warn(args ...any) {
	l.log(LevelWarning, "%s", fmt.Sprint(args...))
}

// Warnf logs a formatted message at Warning level.
func (l *Logger) Warnf(format string, args ...any) {
	l.log(LevelWarning, format, args...)
}

// Info logs a message at Info level.
func (l *Logger) Info(args ...any) {
	l.log(LevelInfo, "%s", fmt.Sprint(args...))
}

// Infof logs a formatted message at Info level.
func (l *Logger) Infof(format string, args ...any) {
	l.log(LevelInfo, format, args...)
}

// Debug logs a message at Debug level.
func (l *Logger) Debug(args ...any) {
	l.log(LevelDebug, "%s", fmt.Sprint(args...))
}

// Debugf logs a formatted message at Debug level.
func (l *Logger) Debugf(format string, args ...any) {
	l.log(LevelDebug, format, args...)
}

// Verbose logs a message at Verbose level.
func (l *Logger) Verbose(args ...any) {
	l.log(LevelVerbose, "%s", fmt.Sprint(args...))
}

// Verbosef logs a formatted message at Verbose level.
func (l *Logger) Verbosef(format string, args ...any) {
	l.log(LevelVerbose, format, args...)
}

// DefaultLogger is the global logger instance used by package-level functions.
var DefaultLogger = New(LevelInfo, os.Stderr)

// SetMaxLevel sets the maximum logging level on the DefaultLogger.
func SetMaxLevel(level Level) {
	DefaultLogger.SetMaxLevel(level)
}

// GetMaxLevel returns the current maximum logging level from the DefaultLogger.
func GetMaxLevel() Level {
	return DefaultLogger.GetMaxLevel()
}

// SetOutput sets the output destination for the DefaultLogger.
func SetOutput(w io.Writer) {
	DefaultLogger.SetOutput(w)
}

// Fatal logs a message at Fatal level using the DefaultLogger and exits.
func Fatal(args ...any) {
	DefaultLogger.Fatal(args...)
}

// Fatalf logs a formatted message at Fatal level using the DefaultLogger and exits.
func Fatalf(format string, args ...any) {
	DefaultLogger.Fatalf(format, args...)
}

// Error logs a message at Error level using the DefaultLogger.
func Error(args ...any) {
	DefaultLogger.Error(args...)
}

// Errorf logs a formatted message at Error level using the DefaultLogger.
func Errorf(format string, args ...any) {
	DefaultLogger.Errorf(format, args...)
}

// Warn logs a message at Warning level using the DefaultLogger.
func Warn(args ...any) {
	DefaultLogger.Warn(args...)
}

// Warnf logs a formatted message at Warning level using the DefaultLogger.
func Warnf(format string, args ...any) {
	DefaultLogger.Warnf(format, args...)
}

// Info logs a message at Info level using the DefaultLogger.
func Info(args ...any) {
	DefaultLogger.Info(args...)
}

// Infof logs a formatted message at Info level using the DefaultLogger.
func Infof(format string, args ...any) {
	DefaultLogger.Infof(format, args...)
}

// Debug logs a message at Debug level using the DefaultLogger.
func Debug(args ...any) {
	DefaultLogger.Debug(args...)
}

// Debugf logs a formatted message at Debug level using the DefaultLogger.
func Debugf(format string, args ...any) {
	DefaultLogger.Debugf(format, args...)
}

// Verbose logs a message at Verbose level using the DefaultLogger.
func Verbose(args ...any) {
	DefaultLogger.Verbose(args...)
}

// Verbosef logs a formatted message at Verbose level using the DefaultLogger.
func Verbosef(format string, args ...any) {
	DefaultLogger.Verbosef(format, args...)
}
