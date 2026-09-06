//go:build windows

package fsutil

import "syscall"

// errCrossDevice is the error a rename fails with when source.
var errCrossDevice error = syscall.Errno(0x11) // ERROR_NOT_SAME_DEVICE
