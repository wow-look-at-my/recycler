//go:build unix

package fsutil

import "syscall"

// errCrossDevice is the error a rename fails with when source and destination
// are on different filesystems.
var errCrossDevice error = syscall.EXDEV
