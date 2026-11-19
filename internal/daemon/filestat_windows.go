//go:build windows

// filestat_windows.go holds statOf.

package daemon

import (
	"io/fs"
	"syscall"
	"time"
)

// statOf answers what the tmp sweep needs to know about a file. Windows gives
// no filesystem number here, and a file this user cannot delete fails the delete.
func statOf(info fs.FileInfo) (dev uint64, mine bool, used time.Time) {
	used = info.ModTime()
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		if at := time.Unix(0, d.LastAccessTime.Nanoseconds()); at.After(used) {
			used = at
		}
	}
	return 0, true, used
}
