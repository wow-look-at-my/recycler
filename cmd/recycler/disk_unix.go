//go:build unix

// disk_unix.go holds diskOf for systems with device numbers.

package main

import "github.com/wow-look-at-my/recycler/internal/fsutil"

// diskOf names the filesystem dir sits on by its mount point.
func diskOf(dir string) string {
	dev, err := fsutil.DeviceOf(dir)
	if err != nil {
		return dir
	}
	return fsutil.TopDirOf(dir, dev)
}
