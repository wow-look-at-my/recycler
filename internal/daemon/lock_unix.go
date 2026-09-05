//go:build unix

package daemon

import (
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes the daemon's exclusive lock without blocking. It reports
// ok=false when another process already holds it, which is how a second daemon
// discovers it has nothing to do.
//
// The lock is the open file description, not the path: it is released when the
// returned function runs or the process dies, so a daemon killed without
// cleanup leaves nothing stale behind.
func tryLock(path string) (unlock func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if err == unix.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, true, nil
}
