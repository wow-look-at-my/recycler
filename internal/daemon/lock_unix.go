//go:build unix

package daemon

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// flockFn is what tryLock locks with, so a test can answer ENOSYS from it.
var flockFn = unix.Flock

// tryLock takes the daemon's exclusive lock without blocking. Each failure
// names the call that produced it: a bare errno here reaches the user as
// "function not implemented" with nothing saying which operation is missing.
func tryLock(path string) (unlock func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("opening the daemon lock %s: %w", path, err)
	}
	if err := flockFn(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if err == unix.EWOULDBLOCK {
			return nil, false, nil
		}
		if lockUnsupported(err) {
			return tryPidLock(path + ".pid")
		}
		return nil, false, fmt.Errorf("flock on %s: %w", path, err)
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, true, nil
}
