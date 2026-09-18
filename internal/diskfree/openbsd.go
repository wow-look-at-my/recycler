//go:build openbsd

package diskfree

import "golang.org/x/sys/unix"

// Free reports the available and total bytes of the filesystem holding path.
// Total counts only blocks this caller can reach; see [reachable]. OpenBSD
// spells the available count signed and lets it go negative once the reserve is
// eaten into, which reads here as nothing available and no reserve.
func Free(path string) (avail, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := uint64(st.F_bsize)
	if st.F_bavail < 0 {
		return 0, uint64(st.F_blocks) * block, nil
	}
	return uint64(st.F_bavail) * block, reachable(st.F_blocks, st.F_bfree, st.F_bavail) * block, nil
}
