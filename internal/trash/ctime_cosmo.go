//go:build cosmo

package trash

import (
	"syscall"
	"time"
)

// A cosmo build carries the Linux Stat_t on every host, which spells it Ctim.
func ctime(st *syscall.Stat_t) time.Time { return time.Unix(st.Ctim.Unix()) }
