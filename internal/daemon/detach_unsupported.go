//go:build !unix && !windows

package daemon

import "os/exec"

// detach has nothing to do on a platform with no recycle bin: EnsureDaemon
// fails on the lock before it reaches this.
func detach(*exec.Cmd) {}
