//go:build linux

package daemon

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// daemonNice is the priority the sweep runs at: pressure arrives on a busy machine.
const daemonNice = -5

// raisePriority puts every thread of this process at daemonNice and reports whether any took it.
// A negative nice needs CAP_SYS_NICE, so the daemon runs either way.
//
// Linux setpriority(PRIO_PROCESS) names a THREAD, and the Go runtime spreads a sweep over threads
// it makes itself. A thread clones the priority of its maker, so covering them all covers the rest.
func raisePriority() bool {
	granted := false
	// A thread made mid-sweep clones from a covered thread, or is covered on the next pass.
	for range 2 {
		tasks, err := os.ReadDir("/proc/self/task")
		if err != nil {
			return unix.Setpriority(unix.PRIO_PROCESS, 0, daemonNice) == nil
		}
		for _, task := range tasks {
			tid, err := strconv.Atoi(task.Name())
			if err != nil {
				continue
			}
			if unix.Setpriority(unix.PRIO_PROCESS, tid, daemonNice) == nil {
				granted = true
			}
		}
	}
	return granted
}
