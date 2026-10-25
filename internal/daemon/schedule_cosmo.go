//go:build cosmo

// schedule keeps the sweep on the efficiency cores of a Mac, and raises it anywhere else.

package daemon

import "github.com/wow-look-at-my/recycler/internal/trash"

// schedule keeps the sweep on the efficiency cores of a Mac, and raises it
// anywhere else.
func schedule() bool {
	if trash.OnMacOS() {
		return background()
	}
	return raisePriority()
}
