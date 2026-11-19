//go:build linux || android || openbsd || solaris || illumos || cosmo

// tmpstat_atim.go holds accessTime.

package daemon

import (
	"syscall"
	"time"
)

// accessTime answers the last access time a stat recorded.
func accessTime(st *syscall.Stat_t) time.Time { return time.Unix(st.Atim.Unix()) }
