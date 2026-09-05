//go:build !unix && !windows

package recycler

// tryLockDaemon has no implementation on a platform with no recycle bin, where
// there is nothing for a daemon to guard.
func tryLockDaemon(string) (unlock func(), ok bool, err error) {
	return nil, false, ErrUnsupported
}
