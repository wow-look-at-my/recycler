//go:build netbsd || solaris

package diskfree

import "golang.org/x/sys/unix"

// Free reports the available and total bytes of the filesystem holding path.
func Free(path string) (avail, total uint64, err error) {
	var st unix.Statvfs_t
	if err := unix.Statvfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := uint64(st.Bsize)
	return uint64(st.Bavail) * block, uint64(st.Blocks) * block, nil
}
