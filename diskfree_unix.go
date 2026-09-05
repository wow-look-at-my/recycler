//go:build unix

package recycler

import "golang.org/x/sys/unix"

// diskFree reports the available and total bytes of the filesystem holding
// path. Available is what an unprivileged process may still write, which is
// what df's "Avail" column shows and is smaller than free space on a
// filesystem holding blocks back for root.
func diskFree(path string) (avail, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := uint64(st.Bsize)
	return uint64(st.Bavail) * block, uint64(st.Blocks) * block, nil
}
