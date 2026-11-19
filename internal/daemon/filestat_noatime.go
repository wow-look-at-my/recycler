//go:build (aix || hurd) && !cosmo

// These systems give no access time this package reads.

package daemon

import (
	"syscall"
	"time"
)

// accessTime answers the zero time, so the modification time stands for the last use.
func accessTime(*syscall.Stat_t) time.Time { return time.Time{} }
