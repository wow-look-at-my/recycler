package daemon

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detach gives the daemon no console and its own process group, so closing the window that ran the
// recycle.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
}
