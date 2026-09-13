//go:build linux || darwin || freebsd || dragonfly

package diskfree

import "golang.org/x/sys/unix"

// Free reports the available and total bytes of the filesystem holding path.
//
// Total counts only blocks this caller can reach. A reserve shows up in Blocks
// and Bfree but not in Bavail, so Blocks alone can describe a pool the caller
// never gets any of, and a target read off it asks for space that never
// arrives. Subtracting the reserve makes total mean what Bavail means, which is
// what GetDiskFreeSpaceEx already reports on Windows.
func Free(path string) (avail, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := uint64(st.Bsize)
	return uint64(st.Bavail) * block, reachable(st.Blocks, st.Bfree, st.Bavail) * block, nil
}
