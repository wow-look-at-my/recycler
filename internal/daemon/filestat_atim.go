//go:build linux || android || openbsd || solaris || illumos || dragonfly || cosmo

// These systems spell the access time Atim.

package daemon

import (
	"syscall"
	"time"
)

// accessTime answers the last access time a stat recorded.
func accessTime(st *syscall.Stat_t) time.Time { return time.Unix(st.Atim.Unix()) }
