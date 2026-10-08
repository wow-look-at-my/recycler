//go:build cosmo

package trash

import (
	"os"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// macOSHost names a directory every Mac has and no other host does.
const macOSHost = "/System/Library/CoreServices"

// Backend picks the bin for the host this binary is running on.
func Backend() (bin.Backend, error) {
	if st, err := os.Stat(macOSHost); err == nil && st.IsDir() {
		return macBackend()
	}
	return fdoBackend()
}
