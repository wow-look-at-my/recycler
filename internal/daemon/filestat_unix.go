//go:build unix

// tmpstat_unix.go holds statOf.

package daemon

import (
	"io/fs"
	"os"
	"syscall"
	"time"
)

// statOf answers what the tmp sweep needs to know about a file. The filesystem
// it sits on, whether this user owns it, and when it was last used.
func statOf(info fs.FileInfo) (dev uint64, mine bool, used time.Time) {
	used = info.ModTime()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false, used
	}
	if at := accessTime(st); at.After(used) {
		used = at
	}
	return uint64(st.Dev), int(st.Uid) == os.Getuid(), used
}
