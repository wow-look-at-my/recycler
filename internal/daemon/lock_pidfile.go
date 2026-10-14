//go:build unix

package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// A system with no flock reports "function not implemented" under an ordinary
// recycle, so the lock falls through to a pid file here.

// lockUnsupported reports that this system cannot flock. EINVAL stays out: a
// bad argument is this package's own defect.
func lockUnsupported(err error) bool {
	return err == syscall.ENOSYS || err == syscall.EOPNOTSUPP
}

// tryPidLock claims path by creating it exclusively, with this process's id
// inside. It answers ok=false when a live daemon already holds it.
func tryPidLock(path string) (unlock func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprintf(f, "%d\n", os.Getpid())
		f.Close()
		return func() { os.Remove(path) }, true, nil
	}
	if !os.IsExist(err) {
		return nil, false, fmt.Errorf("claiming the daemon lock %s: %w", path, err)
	}
	if holderAlive(path) {
		return nil, false, nil
	}
	// The recorded process is gone, so the file is its remains. Removing it
	// races another recycle doing the same, and the loser reads the winner's.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("clearing the stale daemon lock %s: %w", path, err)
	}
	f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, false, nil
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	f.Close()
	return func() { os.Remove(path) }, true, nil
}

// holderAlive reports whether the process named in path still answers. An
// unreadable or unparseable file reads as a live holder. Refusing to start
// costs a poll, and a surplus daemon costs sweeps racing over the same items.
func holderAlive(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return true
	}
	// A kill with no signal delivers nothing and reports whether the pid exists.
	return syscall.Kill(pid, 0) == nil
}
