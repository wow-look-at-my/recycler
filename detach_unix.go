//go:build unix

package recycler

import (
	"os/exec"
	"syscall"
)

// detach puts the daemon in its own session, so it outlives the shell that ran
// the recycle and takes no signal aimed at that shell's process group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
