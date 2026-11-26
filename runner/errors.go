package runner

import (
	"errors"
	"fmt"
)

// errAllSourcesFailed marks a target that no source could search: every
// source returned an error and none returned data.
var errAllSourcesFailed = errors.New("every source failed")

// ConfigError is a usage or configuration problem found before any request
// is sent, such as an unknown source or a source without its API key. The
// CLI exits with code 2 for it.
type ConfigError struct {
	Err error
}

func (e *ConfigError) Error() string { return e.Err.Error() }

func (e *ConfigError) Unwrap() error { return e.Err }

func configErrorf(format string, args ...any) error {
	return &ConfigError{Err: fmt.Errorf(format, args...)}
}
