//go:build darwin && !cosmo

// Backend answers the macOS bin, which is this host's own.

package trash

import "github.com/wow-look-at-my/recycler/internal/bin"

// Backend answers the macOS bin, which is this host's own.
func Backend() (bin.Backend, error) { return macBackend() }
