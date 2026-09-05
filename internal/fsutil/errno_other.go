//go:build !unix && !windows

package fsutil

import "errors"

// errCrossDevice is unused on platforms without a recycle bin implementation,
// but keeps the shared filesystem helpers compiling everywhere.
var errCrossDevice = errors.New("recycler: cross-device rename")
