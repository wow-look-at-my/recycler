//go:build windows

package recycler

import "syscall"

// errCrossDevice is the error a rename fails with when source and destination
// are on different volumes.
var errCrossDevice error = syscall.Errno(0x11) // ERROR_NOT_SAME_DEVICE
