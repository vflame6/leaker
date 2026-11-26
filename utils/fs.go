package utils

import (
	"errors"
	"github.com/mattn/go-isatty"
	"os"
	"path/filepath"
)

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

// CreateOutputFile creates the -o file, readable by its owner only, and any
// missing parent directories (mode 0700). An existing file is an error
// wrapping fs.ErrExist unless overwrite is true; then it is truncated.
func CreateOutputFile(filename string, overwrite bool) (*os.File, error) {
	if filename == "" {
		return nil, errors.New("empty filename")
	}
	if dir := filepath.Dir(filename); dir != "" {
		if err := os.MkdirAll(dir, PrivateDirMode); err != nil {
			return nil, err
		}
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	return os.OpenFile(filename, flags, PrivateFileMode)
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
