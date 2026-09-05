package recycler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DaemonLockPath returns the file whose lock names the running daemon. One
// daemon holds it per user, which is what keeps a recycle every few seconds
// from starting a daemon every few seconds.
func DaemonLockPath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("recycler: locating cache directory: %w", err)
	}
	dir := filepath.Join(cache, "recycler")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.lock"), nil
}

// EnsureDaemon starts the disk-pressure daemon if one is not already running,
// by running exe with the "daemon" argument detached from this process. It
// reports whether it started one.
//
// Spawning is the caller's decision rather than something [Recycle] does: this
// package is a library, and a library that forks a process out of an ordinary
// call is not something a program can be expected to want. The CLI calls this
// after recycling, which is what makes the daemon appear on first use of the
// tool.
func EnsureDaemon(exe string) (bool, error) {
	// A test binary is never the daemon. os.Executable() under `go test` is
	// the test binary, and running one with a "daemon" argument runs the whole
	// suite again - which recycles something, which lands back here and starts
	// another. That is a fork bomb built out of the caller's own binary, and
	// the lock does not stop it: each generation takes the lock, finds it free
	// because the last one exited, and spawns.
	if strings.HasSuffix(filepath.Base(exe), ".test") {
		return false, fmt.Errorf("recycler: refusing to start a daemon from a test binary (%s)", exe)
	}

	lock, err := DaemonLockPath()
	if err != nil {
		return false, err
	}
	// Holding the lock means nobody else does, so no daemon is running. The
	// spawned daemon takes it for itself, and this releases it either way:
	// two racing spawns both land here, and the loser exits on the lock.
	unlock, free, err := tryLockDaemon(lock)
	if err != nil {
		return false, err
	}
	if !free {
		return false, nil
	}
	unlock()

	cmd := exec.Command(exe, "daemon")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("recycler: starting daemon: %w", err)
	}
	// Nothing waits for it. Releasing the handle is what keeps it from
	// staying a zombie for as long as this process lives.
	go cmd.Process.Release()
	return true, nil
}
