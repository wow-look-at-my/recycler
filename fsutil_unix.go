//go:build unix

package recycler

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// deviceOf returns the device number of the filesystem holding path.
func deviceOf(path string) (uint64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("recycler: cannot determine the filesystem of %s", path)
	}
	return uint64(st.Dev), nil
}

// topDirOf walks up from dir for as long as the device number stays the same,
// yielding the mount point of the filesystem dir lives on.
func topDirOf(dir string, dev uint64) string {
	current := filepath.Clean(dir)
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return current
		}
		parentDev, err := deviceOf(parent)
		if err != nil || parentDev != dev {
			return current
		}
		current = parent
	}
}
