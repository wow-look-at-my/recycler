//go:build !unix && !windows

package recycler

import "errors"

// errCrossDevice is unused on platforms without a recycle bin implementation,
// but keeps the shared filesystem helpers compiling everywhere.
var errCrossDevice = errors.New("recycler: cross-device rename")
