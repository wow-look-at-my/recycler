//go:build cosmo

package trash

import (
	"os"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// GOOS reads "cosmo" on every host, so the bin is chosen here, not at build
// time. Choosing wrong fills the other platform's bin.

// macOSHost names a directory every Mac has and no other host does.
const macOSHost = "/System/Library/CoreServices"

// Backend picks the bin for the host this binary is running on.
func Backend() (bin.Backend, error) {
	if st, err := os.Stat(macOSHost); err == nil && st.IsDir() {
		return macBackend()
	}
	return fdoBackend()
}
