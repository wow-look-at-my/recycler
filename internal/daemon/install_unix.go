//go:build unix

package daemon

import (
	"fmt"
	"os/exec"
	"strings"
)

// Install registers exe's daemon with the service manager of the kernel this
// runs on, so it starts at login and is restarted whenever it exits. It returns
// the file it wrote.
func Install(exe string) (string, error) {
	kernel, err := exec.Command("uname", "-s").Output()
	if err != nil {
		return "", fmt.Errorf("recycler: asking the kernel its name: %w", err)
	}
	s, err := newService(exe)
	if err != nil {
		return "", err
	}
	return s.install(strings.TrimSpace(string(kernel)))
}
