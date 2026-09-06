//go:build !linux && !darwin && !windows && !freebsd && !netbsd && !openbsd && !dragonfly && !solaris

package trash

import (
	"fmt"
	"runtime"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// Backend reports that this GOOS has no recycle bin implementation.
func Backend() (bin.Backend, error) {
	return nil, fmt.Errorf("%w: %s", bin.ErrUnsupported, runtime.GOOS)
}
