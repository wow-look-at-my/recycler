//go:build !darwin && !cosmo

// schedule raises the sweep above a busy machine's work.

package daemon

// schedule raises the sweep above a busy machine's work.
func schedule() bool { return raisePriority() }
