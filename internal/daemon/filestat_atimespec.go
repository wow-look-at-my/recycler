//go:build (darwin || ios || freebsd || netbsd) && !cosmo

// These systems spell the access time Atimespec.

package daemon

import (
	"syscall"
	"time"
)

// accessTime answers the last access time a stat recorded.
func accessTime(st *syscall.Stat_t) time.Time { return time.Unix(st.Atimespec.Unix()) }
