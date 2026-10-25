//go:build cosmo

package trash

import (
	"os"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// macOSHost names a directory every Mac has and no other host does.
const macOSHost = "/System/Library/CoreServices"

// OnMacOS reports whether the host this binary is running on is a Mac.
func OnMacOS() bool {
	st, err := os.Stat(macOSHost)
	return err == nil && st.IsDir()
}

// Backend picks the bin for the host this binary is running on.
func Backend() (bin.Backend, error) {
	if OnMacOS() {
		return macBackend()
	}
	return fdoBackend()
}
