//go:build darwin

package trash

import (
	"syscall"
	"time"
)

// Apple spells the inode change time Ctimespec.
func ctime(st *syscall.Stat_t) time.Time { return time.Unix(st.Ctimespec.Unix()) }
