//go:build unix

// DeviceOf returns the device number of the filesystem holding path.

package fsutil

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// DeviceOf returns the device number of the filesystem holding path.
func DeviceOf(path string) (uint64, error) {
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

// SameFilesystem reports whether a and b sit on one filesystem, where a
// rename between them moves no data and needs no free space.
func SameFilesystem(a, b string) (bool, error) {
	devA, err := DeviceOf(a)
	if err != nil {
		return false, err
	}
	devB, err := DeviceOf(b)
	if err != nil {
		return false, err
	}
	return devA == devB, nil
}

// Allocated is the space an entry takes on its filesystem: its st_blocks, in
// 512-byte units whatever the filesystem's block size.
func Allocated(info fs.FileInfo) int64 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size()
	}
	return int64(st.Blocks) * 512
}

// IsStickyDir reports whether path is a real directory, and not a symlink to a directory.
func IsStickyDir(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 && fi.Mode()&os.ModeSticky != 0
}

// TopDirOf walks up from dir for as long as the device number stays the same.
func TopDirOf(dir string, dev uint64) string {
	current := filepath.Clean(dir)
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return current
		}
		parentDev, err := DeviceOf(parent)
		if err != nil || parentDev != dev {
			return current
		}
		current = parent
	}
}
