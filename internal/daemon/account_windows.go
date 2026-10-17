package daemon

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// accountHome answers the profile directory the shell records for this user.
// USERPROFILE is never read.
func accountHome() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Profile, 0)
	if err != nil {
		return "", fmt.Errorf("recycler: locating the profile directory: %w", err)
	}
	return dir, nil
}

// accountCacheDir answers the local application data directory. LOCALAPPDATA
// is never read.
func accountCacheDir() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", fmt.Errorf("recycler: locating the local application data directory: %w", err)
	}
	return dir, nil
}

// UseAccountEnvironment has nothing to do here: the Windows recycle bin is on
// each drive, and no variable moves it.
func UseAccountEnvironment() error { return nil }
