//go:build darwin

// schedule keeps the sweep on the efficiency cores.

package daemon

// schedule keeps the sweep on the efficiency cores.
func schedule() bool { return background() }
