//go:build unix

// errCrossDevice is the error a rename.

package fsutil

import "syscall"

// errCrossDevice is the error a rename.
var errCrossDevice error = syscall.EXDEV
