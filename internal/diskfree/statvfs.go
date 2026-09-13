//go:build netbsd || solaris

package diskfree

import "golang.org/x/sys/unix"

// Free reports the available and total bytes of the filesystem holding path.
// Total counts only blocks this caller can reach; see [reachable].
func Free(path string) (avail, total uint64, err error) {
	var st unix.Statvfs_t
	if err := unix.Statvfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := uint64(st.Bsize)
	return uint64(st.Bavail) * block, reachable(st.Blocks, st.Bfree, st.Bavail) * block, nil
}
