//go:build openbsd

package diskfree

import "golang.org/x/sys/unix"

// Free reports the available and total bytes of the filesystem holding
// path. OpenBSD spells the statfs fields with an F_ prefix, and its available
// count is signed: a filesystem overdrawn past its root reserve reports a
// negative number, which is no room at all rather than an enormous amount.
func Free(path string) (avail, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := uint64(st.F_bsize)
	if st.F_bavail < 0 {
		return 0, uint64(st.F_blocks) * block, nil
	}
	return uint64(st.F_bavail) * block, uint64(st.F_blocks) * block, nil
}
