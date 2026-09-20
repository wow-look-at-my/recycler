package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LockPath returns the file whose lock names the running daemon.
func LockPath() (string, error) { return statePath("daemon.lock") }

// LogPath is a detached daemon's only way to say what it destroyed.
func LogPath() (string, error) { return statePath("daemon.log") }

func statePath(name string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("recycler: locating cache directory: %w", err)
	}
	dir := filepath.Join(cache, "recycler")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("recycler: creating %s: %w", dir, err)
	}
	return filepath.Join(dir, name), nil
}

// maxLogBytes keeps the log from being what fills the disk. Truncated, never
// rotated: a rotation keeps another copy of what nobody read.
const maxLogBytes = 1 << 20

// openLog returns the detached daemon's log, truncated if it has grown past its
// bound. A log that cannot be opened is not fatal: sweeping matters more than
// reporting, and the caller falls back to discarding output.
func openLog() *os.File {
	path, err := LogPath()
	if err != nil {
		return nil
	}
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogBytes {
		os.Truncate(path, 0)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	return f
}

func Ensure(exe string) (bool, error) {
	// A test binary is never the daemon.
	if strings.HasSuffix(filepath.Base(exe), ".test") {
		return false, fmt.Errorf("recycler: refusing to start a daemon from a test binary (%s)", exe)
	}

	lock, err := LockPath()
	if err != nil {
		return false, err
	}
	// Holding the lock means nobody else does, so no daemon is running.
	unlock, free, err := tryLock(lock)
	if err != nil {
		return false, err
	}
	if !free {
		return false, nil
	}
	unlock()

	cmd := exec.Command(exe, "daemon")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if log := openLog(); log != nil {
		defer log.Close()
		cmd.Stdout, cmd.Stderr = log, log
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("recycler: starting daemon: %w", err)
	}
	// Nothing waits for it.
	go cmd.Process.Release()
	return true, nil
}
