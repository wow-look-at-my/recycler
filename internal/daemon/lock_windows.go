package daemon

import (
	"os"

	"golang.org/x/sys/windows"
)

// tryLock takes the daemon's exclusive lock without.
func tryLock(path string) (unlock func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	h := windows.Handle(f.Fd())
	var overlapped windows.Overlapped
	err = windows.LockFileEx(h,
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped)
	if err != nil {
		f.Close()
		if err == windows.ERROR_LOCK_VIOLATION {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		var o windows.Overlapped
		windows.UnlockFileEx(h, 0, 1, 0, &o)
		f.Close()
	}, true, nil
}
