package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LockPath returns the file whose lock names the running daemon.
func LockPath() (string, error) {
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

func Ensure(exe string) (bool, error) {
	// A test binary is never the daemon.
	if strings.HasSuffix(filepath.Base(exe), ".test") {
		return false, fmt.Errorf("recycler: refusing to start a daemon from a test binary (%s)", exe)
	}

	lock, err := LockPath()
	if err != nil {
		return false, err
	}

	// The test below frees the daemon lock, because the child takes it. This guards that gap.
	endSpawn, alone, err := tryLock(lock + ".spawn")
	if err != nil {
		return false, err
	}
	if !alone {
		return false, nil
	}
	defer endSpawn()

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
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("recycler: starting daemon: %w", err)
	}
	// Nothing waits for it.
	go cmd.Process.Release()

	// Waiting here hands the next caller a lock the child has already taken.
	waitUntilHeld(lock, spawnHandoffTimeout)
	return true, nil
}

// spawnHandoffTimeout bounds the wait for the child to take the lock. A child that died leaves it
// free, and the next caller then starts a daemon rather than standing down forever.
const spawnHandoffTimeout = 5 * time.Second

// waitUntilHeld returns when somebody else holds the lock, or when the timeout runs out.
func waitUntilHeld(path string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		unlock, free, err := tryLock(path)
		if err != nil {
			return
		}
		if !free {
			return
		}
		unlock()
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
