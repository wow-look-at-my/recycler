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

// isStickyDir reports whether path is a real directory - not a symlink to one -
// with the sticky bit set, which is what makes a shared trash directory safe
// for several users.
func isStickyDir(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 && fi.Mode()&os.ModeSticky != 0
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
