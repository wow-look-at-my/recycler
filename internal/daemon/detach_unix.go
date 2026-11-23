//go:build unix

// detach puts the daemon in its own session, so it outlives the shell.

package daemon

import (
	"os/exec"
	"syscall"
)

// detach puts the daemon in its own session, so it outlives the shell.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
