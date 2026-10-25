//go:build !unix && !windows

// errCrossDevice is unused on platforms without a recycle bin.

package fsutil

import "errors"

// errCrossDevice is unused on platforms without a recycle bin.
var errCrossDevice = errors.New("recycler: cross-device rename")
