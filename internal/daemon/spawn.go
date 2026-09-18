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
func LockPath() (string, error) { return statePath("daemon.lock") }

// LogPath returns the file a detached daemon writes its reports to. A daemon
// nobody started from a terminal has nowhere else to say what it destroyed or
// what it could not keep up with, and discarding that leaves the a single
// account of both with no reader.
func LogPath() (string, error) { return statePath("daemon.log") }

func statePath(name string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("recycler: locating cache directory: %w", err)
	}
	dir := filepath.Join(cache, "recycler")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// maxLogBytes keeps the log from becoming the thing that fills the disk. It is
// truncated rather than rotated, because this file is read after something went
// wrong and a rotation would keep another copy of what nobody read.
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
	me := selfIdentity(exe)
	replacing, known := Identity{}, false
	if free {
		unlock()
	} else {
		// Somebody is sweeping. Only a newer build displaces it, and a daemon
		// that recorded no build is left alone rather than guessed about.
		replacing, known = Running()
		if !known || !Newer(me, replacing) {
			return false, nil
		}
	}

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

	if known {
		// The child has to get the lock out of the running daemon before it can
		// sweep, and that daemon only lets go on its own next tick.
		if !waitForSuccessor(me, handoverTimeout) {
			return false, fmt.Errorf("recycler: started %s to replace %s, which did not hand over within %s",
				me, replacing, handoverTimeout)
		}
		return true, nil
	}

	// Waiting here hands the next caller a lock the child has already taken.
	lockTakenByAnother(lock, spawnHandoffTimeout)
	return true, nil
}

// spawnHandoffTimeout bounds the wait for the child to take the lock. A child that died leaves it
// free, and the next caller then starts a daemon rather than standing down forever.
const spawnHandoffTimeout = 5 * time.Second
