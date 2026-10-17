package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// ReapStrays stops every other daemon of this user that sweeps under a lock
// other than lock, and returns the pids it signalled. Only a daemon holding the
// lock calls this. Such a stray comes from a build that kept its lock under the
// caller's environment, and it never stops by itself.
//
// /proc lists only this PID namespace, so a daemon in a container is never seen.
func ReapStrays(lock string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if os.IsNotExist(err) {
		// A portable build runs this file on a kernel with no /proc.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	return reapAmong(lock, pids)
}

// reapAmong is ReapStrays over a stated set of pids, so a test never reaches a
// daemon it did not start.
func reapAmong(lock string, pids []int) ([]int, error) {
	mine, err := os.Stat(lock)
	if err != nil {
		return nil, err
	}
	uid, self := os.Getuid(), os.Getpid()
	var reaped []int
	for _, pid := range pids {
		if pid == self || !ownedBy(pid, uid) || !runsDaemon(pid) || !holdsOtherLock(pid, mine) {
			continue
		}
		if syscall.Kill(pid, syscall.SIGTERM) == nil {
			reaped = append(reaped, pid)
		}
	}
	return reaped, nil
}

func ownedBy(pid, uid int) bool {
	st, err := os.Stat("/proc/" + strconv.Itoa(pid))
	if err != nil {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == uid
}

// runsDaemon reports whether the command line is the sweep loop. The up and
// install subcommands return at once, and --once is one sweep.
func runsDaemon(pid int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return false
	}
	args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	i := slices.Index(args, "daemon")
	if i < 1 {
		return false
	}
	for _, arg := range args[i+1:] {
		if arg == "up" || arg == "install" || arg == "--once" {
			return false
		}
	}
	return true
}

// holdsOtherLock reports whether pid has a daemon lock open that is not mine.
// A process with mine open is waiting for it, which is a handover in progress.
func holdsOtherLock(pid int, mine os.FileInfo) bool {
	fdDir := "/proc/" + strconv.Itoa(pid) + "/fd"
	fds, err := os.ReadDir(fdDir)
	if err != nil {
		return false
	}
	other := false
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
		if err != nil {
			continue
		}
		target = strings.TrimSuffix(target, " (deleted)")
		if filepath.Base(target) != "daemon.lock" || filepath.Base(filepath.Dir(target)) != "recycler" {
			continue
		}
		st, err := os.Stat(filepath.Join(fdDir, fd.Name()))
		if err != nil {
			continue
		}
		if os.SameFile(st, mine) {
			return false
		}
		other = true
	}
	return other
}
