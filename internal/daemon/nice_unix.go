//go:build unix && !linux

package daemon

import "golang.org/x/sys/unix"

// daemonNice is the priority the sweep runs at: pressure arrives on a busy machine.
const daemonNice = -5

// raisePriority asks for daemonNice and reports whether it was granted. A
// negative nice needs CAP_SYS_NICE, so the daemon runs either way.
func raisePriority() bool {
	return unix.Setpriority(unix.PRIO_PROCESS, 0, daemonNice) == nil
}
