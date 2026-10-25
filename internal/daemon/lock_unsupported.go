//go:build !unix && !windows

// tryLock has no implementation on a platform with no recycle bin, where.

package daemon

import "github.com/wow-look-at-my/recycler/internal/bin"

// tryLock has no implementation on a platform with no recycle bin, where.
func tryLock(string) (unlock func(), ok bool, err error) {
	return nil, false, bin.ErrUnsupported
}
