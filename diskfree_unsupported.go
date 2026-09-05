//go:build !linux && !darwin && !freebsd && !dragonfly && !openbsd && !netbsd && !solaris && !windows

package recycler

// diskFree has no implementation on a platform with no recycle bin either, so
// the daemon reports the same ErrUnsupported every other operation does.
func diskFree(string) (avail, total uint64, err error) {
	return 0, 0, ErrUnsupported
}
