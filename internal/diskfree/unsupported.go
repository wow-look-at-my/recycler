//go:build !linux && !darwin && !freebsd && !dragonfly && !openbsd && !netbsd && !solaris && !windows

package diskfree

import "github.com/wow-look-at-my/recycler/internal/bin"

// Free has no implementation on a platform with no recycle bin either, so the
// daemon reports the same [bin.ErrUnsupported] every other operation does.
func Free(string) (avail, total uint64, err error) {
	return 0, 0, bin.ErrUnsupported
}
