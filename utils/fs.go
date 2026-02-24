package utils

import (
	"errors"
	"fmt"
	"github.com/mattn/go-isatty"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ParseTargets(targets string, stdin bool) (io.Reader, error) {
	// The tool will use STDIN if specified with CLI arguments
	// STDIN input is preferred because of potential data loss from piped input
	// We consider that CLI input (single email or file) cannot be lost, unlike piped output from other tools
	if stdin {
		return os.Stdin, nil
	}

	if targets != "" {
		// check if targets is a file
		if FileExists(targets) {
			f, err := os.Open(targets)
			if err != nil {
				return nil, err
			}
			return f, nil
		} else {
			// if targets is not a file, process it like a line
			return strings.NewReader(targets), nil
		}
	}

	return nil, errors.New("no targets provided")
}

// UserConfigDirOrDefault returns the user config directory or defaultConfigDir in case of error
func UserConfigDirOrDefault(defaultConfigDir string) string {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return defaultConfigDir
	}
	return userConfigDir
}

// AppConfigDirOrDefault returns the app config directory
func AppConfigDirOrDefault(defaultAppConfigDir string, toolName string) string {
	userConfigDir := UserConfigDirOrDefault("")
	if userConfigDir == "" {
		return filepath.Join(defaultAppConfigDir, toolName)
	}
	return filepath.Join(userConfigDir, toolName)
}

// FileExists checks if the file exists in the provided path
func FileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func CreateFileWithSafe(filename string, appendToFile bool, overwrite bool) (*os.File, error) {
	if filename == "" {
		return nil, errors.New("empty filename")
	}

	if !overwrite && FileExists(filename) {
		return nil, fmt.Errorf("file already exists: %s", filename)
	}

	// create nested directories if they not exist
	dir := filepath.Dir(filename)
	if dir != "" {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			err := os.MkdirAll(dir, os.ModePerm)
			if err != nil {
				return nil, err
			}
		}
	}

	var file *os.File
	var err error
	if appendToFile {
		file, err = os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	} else {
		file, err = os.Create(filename)
	}
	if err != nil {
		return nil, err
	}

	return file, nil
}

// HasStdin determines if the user has piped input
func HasStdin() bool {
	if IsWindows() && (isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())) {
		return false
	}
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	mode := stat.Mode()

	isPipedFromChrDev := (mode & os.ModeCharDevice) == 0
	isPipedFromFIFO := (mode & os.ModeNamedPipe) != 0

	return isPipedFromChrDev || isPipedFromFIFO
}
