//go:build !unix && !windows

package fsutil

import "errors"

// errCrossDevice is unused on platforms without a recycle bin.
var errCrossDevice = errors.New("recycler: cross-device rename")
