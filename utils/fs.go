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
	var readers []io.Reader

	// Add stdin if piped input is detected
	if stdin {
		readers = append(readers, os.Stdin)
	}

	// Add CLI targets (file path or inline value)
	if targets != "" {
		if FileExists(targets) {
			f, err := os.Open(targets)
			if err != nil {
				return nil, err
			}
			readers = append(readers, f)
		} else {
			// Inline target — add newline so scanner reads it as a complete line
			readers = append(readers, strings.NewReader(targets+"\n"))
		}
	}

	if len(readers) == 0 {
		return nil, errors.New("no targets provided")
	}

	return io.MultiReader(readers...), nil
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
