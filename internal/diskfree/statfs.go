//go:build linux || darwin || freebsd || dragonfly

package diskfree

import "golang.org/x/sys/unix"

// Free reports the available and total bytes of the filesystem holding path.
//
// Total counts only what this caller can reach. A reserve sits in Blocks and
// Bfree but not Bavail, so a target read off Blocks never arrives.
func Free(path string) (avail, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := uint64(st.Bsize)
	return uint64(st.Bavail) * block, reachable(st.Blocks, st.Bfree, st.Bavail) * block, nil
}
