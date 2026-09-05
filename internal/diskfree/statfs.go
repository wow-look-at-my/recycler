//go:build linux || darwin || freebsd || dragonfly

package diskfree

import "golang.org/x/sys/unix"

// Free reports the available and total bytes of the filesystem holding
// path. Available is what an unprivileged process may still write, which is
// what df's "Avail" column shows: it is smaller than the free space on a
// filesystem that holds blocks back for root, and the daemon must not count
// room it cannot use.
func Free(path string) (avail, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := uint64(st.Bsize)
	return uint64(st.Bavail) * block, uint64(st.Blocks) * block, nil
}
