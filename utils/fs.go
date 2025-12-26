package utils

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ParseTargets(targets string) (io.Reader, error) {
	if targets == "" {
		return nil, errors.New("targets cannot be empty")
	}

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
