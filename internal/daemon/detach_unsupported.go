//go:build !unix && !windows

package daemon

import "os/exec"

func detach(*exec.Cmd) {}
