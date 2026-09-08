//go:build unix

package daemon

import "golang.org/x/sys/unix"

// daemonNice is the priority the sweep runs at. Disk pressure arrives while the
// machine is busy, and a sweep wants syscalls rather than a core.
const daemonNice = -5

// raisePriority asks for daemonNice and reports whether it was granted. A
// negative nice needs CAP_SYS_NICE, so the daemon runs either way.
func raisePriority() bool {
	return unix.Setpriority(unix.PRIO_PROCESS, 0, daemonNice) == nil
}
