//go:build !linux && !darwin && !windows && !freebsd && !netbsd && !openbsd && !dragonfly && !solaris

package recycler

import (
	"fmt"
	"runtime"
)

// platformBackend reports that this GOOS has no recycle bin implementation.
func platformBackend() (backend, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, runtime.GOOS)
}
