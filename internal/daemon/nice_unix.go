//go:build unix

package daemon

import "golang.org/x/sys/unix"

// daemonNice is the priority the sweep runs at. A build saturating the machine
// is exactly when the bin has to be given back, and the sweep is a few statfs
// calls and some unlinks rather than anything that competes for a core.
const daemonNice = -5

// raisePriority asks for daemonNice, and reports whether it was granted. A
// negative nice needs CAP_SYS_NICE, so an ordinary user keeps the default and
// the daemon runs anyway: sweeping late beats not sweeping.
func raisePriority() bool {
	return unix.Setpriority(unix.PRIO_PROCESS, 0, daemonNice) == nil
}
