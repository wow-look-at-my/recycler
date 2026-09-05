//go:build netbsd || solaris

package recycler

import "golang.org/x/sys/unix"

// diskFree reports the available and total bytes of the filesystem holding
// path. NetBSD and Solaris expose this through statvfs; there is no Statfs_t
// on either.
func diskFree(path string) (avail, total uint64, err error) {
	var st unix.Statvfs_t
	if err := unix.Statvfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := uint64(st.Bsize)
	return uint64(st.Bavail) * block, uint64(st.Blocks) * block, nil
}
