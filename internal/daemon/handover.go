package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// handoverTimeout bounds both halves of a handover: how long the daemon
// standing down waits for its successor to take the lock, and how long the
// successor waits for the lock to come free. A handover nobody completes ends
// with the daemon that was already sweeping still sweeping.
var handoverTimeout = 10 * time.Second

// handoverPoll is how often either half looks. A handover happens when a
// machine gets a new build, so this runs for a moment and then never again.
const handoverPoll = 10 * time.Millisecond

// IdentityPath returns the file the daemon holding the lock records its build
// in. It exists only while that daemon is sweeping.
func IdentityPath() (string, error) { return statePath("daemon.id") }

// handoverPath returns the file a newer daemon leaves to ask the running one to
// stand down. The newer daemon removes it once it holds the lock, which is how
// the one standing down learns the handover completed.
func handoverPath() (string, error) { return statePath("daemon.takeover") }

// Running returns the build of the daemon currently holding the lock.
func Running() (Identity, bool) {
	path, err := IdentityPath()
	if err != nil {
		return Identity{}, false
	}
	return readIdentity(path)
}

func readIdentity(path string) (Identity, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, false
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return Identity{}, false
	}
	return id, true
}

// writeIdentity records a build under path, through a rename so a reader never
// sees half of one.
func writeIdentity(path string, id Identity) error {
	data, err := json.Marshal(id)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// announce records the build now holding the lock, and returns the call that
// withdraws it.
//
// Withdrawing removes the record only while it still names this build. A daemon
// that has handed the lock over has a successor's record to leave alone, and
// erasing it would tell the next caller nobody is sweeping.
func announce(id Identity) func() {
	path, err := IdentityPath()
	if err != nil {
		return func() {}
	}
	writeIdentity(path, id)
	return func() {
		if recorded, ok := readIdentity(path); ok && !sameBuild(recorded, id) {
			return
		}
		os.Remove(path)
	}
}

// requestHandover asks the running daemon to give up the lock.
func requestHandover(id Identity) error {
	path, err := handoverPath()
	if err != nil {
		return err
	}
	return writeIdentity(path, id)
}

// pendingHandover returns the build that has asked for the lock.
func pendingHandover() (Identity, bool) {
	path, err := handoverPath()
	if err != nil {
		return Identity{}, false
	}
	return readIdentity(path)
}

func clearHandover() {
	if path, err := handoverPath(); err == nil {
		os.Remove(path)
	}
}

// takeLock takes the daemon lock for the build in me, asking whoever holds it to
// hand it over when me is newer. It returns the call that releases the lock.
//
// A daemon that recorded no build is left alone: without something to compare
// against, standing down keeps the one daemon that is already sweeping.
func takeLock(lock string, me Identity) (func(), error) {
	unlock, free, err := tryLock(lock)
	if err != nil {
		return nil, err
	}
	if free {
		return unlock, nil
	}

	running, known := Running()
	if !known {
		return nil, fmt.Errorf("%w, and recorded no build to compare against", bin.ErrDaemonRunning)
	}
	if !Newer(me, running) {
		return nil, fmt.Errorf("%w: %s is sweeping, and this build (%s) is not newer",
			bin.ErrDaemonRunning, running, me)
	}
	if err := requestHandover(me); err != nil {
		return nil, err
	}
	unlock, err = waitForLock(lock, handoverTimeout)
	if err != nil {
		return nil, err
	}
	if unlock == nil {
		clearHandover()
		return nil, fmt.Errorf("%w: %s did not hand over within %s", bin.ErrDaemonRunning, running, handoverTimeout)
	}
	return unlock, nil
}

// waitForLock takes the lock as soon as it comes free, and returns a nil unlock
// when it never does.
func waitForLock(path string, timeout time.Duration) (func(), error) {
	deadline := time.Now().Add(timeout)
	for {
		unlock, free, err := tryLock(path)
		if err != nil {
			return nil, err
		}
		if free {
			return unlock, nil
		}
		if time.Now().After(deadline) {
			return nil, nil
		}
		time.Sleep(handoverPoll)
	}
}

// standDown releases the lock to the newer daemon that asked for it, and
// returns nil once that daemon holds it.
//
// It takes the lock back, and returns the call that releases it again, when the
// successor never arrives. A handover nobody completed must not leave the disk
// unwatched, which is the one way this could end with nothing sweeping.
func standDown(lock string, unlock func(), me Identity) func() {
	unlock()
	if successorArrived(lock, handoverTimeout) {
		return nil
	}
	clearHandover()
	resumed, free, err := tryLock(lock)
	if err != nil || !free {
		return nil
	}
	announce(me)
	return resumed
}

// successorArrived reports whether the daemon that asked for the lock took it.
// The request going away is the signal, because the successor withdraws it once
// it holds the lock; the one probe afterwards is what separates that from a
// successor that gave up and withdrew its own request.
//
// Watching the request rather than the lock keeps this out of the successor's
// way: two processes polling one lock can take turns holding it and neither
// gets on with sweeping.
func successorArrived(lock string, timeout time.Duration) bool {
	path, err := handoverPath()
	if err != nil {
		return false
	}
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return !lockFree(lock)
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(handoverPoll)
	}
}

// lockFree reports whether the lock is going unheld right now.
func lockFree(path string) bool {
	unlock, free, err := tryLock(path)
	if err != nil {
		return false
	}
	if free {
		unlock()
	}
	return free
}

// lockTakenByAnother waits until somebody other than this process holds the
// lock, and reports whether that happened before the timeout.
func lockTakenByAnother(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		unlock, free, err := tryLock(path)
		if err != nil {
			return false
		}
		if !free {
			return true
		}
		unlock()
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(handoverPoll)
	}
}

// waitForSuccessor returns when the daemon holding the lock is a build no older
// than me, which is what a completed handover looks like from outside. The
// successor records the executable as it resolves it, so this asks about the
// build rather than an exact match.
func waitForSuccessor(me Identity, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if running, ok := Running(); ok && !Newer(me, running) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(handoverPoll)
	}
}
