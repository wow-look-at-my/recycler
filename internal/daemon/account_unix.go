//go:build unix

package daemon

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
)

// accountHome answers the home directory the account database records for this
// user. HOME is never read: a caller that points it elsewhere must not get a
// daemon of its own.
func accountHome() (string, error) {
	uid := strconv.Itoa(os.Getuid())
	u, err := user.LookupId(uid)
	if err != nil {
		return "", fmt.Errorf("recycler: looking up uid %s in the account database: %w", uid, err)
	}
	if !filepath.IsAbs(u.HomeDir) {
		return "", fmt.Errorf("recycler: the account database gives uid %s no absolute home directory (%q)", uid, u.HomeDir)
	}
	return u.HomeDir, nil
}

// accountCacheDir is where os.UserCacheDir points when nothing overrides it.
func accountCacheDir() (string, error) {
	home, err := accountHome()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches"), nil
	}
	return filepath.Join(home, ".cache"), nil
}

// UseAccountEnvironment gives this process the account's home bin, whoever
// started it. The daemon is one per user, so the bin it watches cannot depend
// on the environment of the caller that happened to start it.
func UseAccountEnvironment() error {
	home, err := accountHome()
	if err != nil {
		return err
	}
	if err := os.Setenv("HOME", home); err != nil {
		return err
	}
	return os.Unsetenv("XDG_DATA_HOME")
}
