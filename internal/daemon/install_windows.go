//go:build windows

// Install registers exe's daemon as a scheduled task, so it starts at logon and is restarted when it fails.

package daemon

import (
	"fmt"
	"os/user"
)

// Install registers exe's daemon as a scheduled task, so it starts at logon and
// is restarted when it fails. It returns the file it wrote.
func Install(exe string) (string, error) {
	s, err := newService(exe)
	if err != nil {
		return "", err
	}
	me, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("recycler: naming the current user: %w", err)
	}
	s.user = me.Username
	return s.install("Windows")
}
