//go:build unix

package fsutil

import "syscall"

// errCrossDevice is the error a rename.
var errCrossDevice error = syscall.EXDEV
