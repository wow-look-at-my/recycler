//go:build !unix && !windows

// tmpstat_other.go holds statOf.

package daemon

import (
	"io/fs"
	"time"
)

// statOf answers the modification time as the last use. This platform gives no owner to check.
func statOf(info fs.FileInfo) (dev uint64, mine bool, used time.Time) {
	return 0, false, info.ModTime()
}
