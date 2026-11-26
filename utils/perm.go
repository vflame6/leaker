package utils

import "os"

// Modes for files and directories that hold API keys or leaked credentials.
const (
	PrivateDirMode  os.FileMode = 0o700
	PrivateFileMode os.FileMode = 0o600
)

// RestrictToOwner removes all group and other permissions from path. It
// returns the previous mode and whether it changed anything. Missing files
// are left alone, and so is everything on Windows, which has no such modes.
func RestrictToOwner(path string) (previous os.FileMode, changed bool, err error) {
	if IsWindows() {
		return 0, false, nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	previous = info.Mode().Perm()
	if previous&0o077 == 0 {
		return previous, false, nil
	}
	if err := os.Chmod(path, previous&^0o077); err != nil {
		return previous, false, err
	}
	return previous, true, nil
}
