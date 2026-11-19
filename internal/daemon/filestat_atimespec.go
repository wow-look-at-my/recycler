//go:build (darwin || ios || freebsd || netbsd || dragonfly) && !cosmo

// tmpstat_atimespec.go holds accessTime.

package daemon

import (
	"syscall"
	"time"
)

// accessTime answers the last access time a stat recorded.
func accessTime(st *syscall.Stat_t) time.Time { return time.Unix(st.Atimespec.Unix()) }
