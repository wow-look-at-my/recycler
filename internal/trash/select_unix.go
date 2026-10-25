//go:build (linux || freebsd || netbsd || openbsd || dragonfly || solaris) && !cosmo

// Backend answers the FreeDesktop bin, which is this host's own.

package trash

import "github.com/wow-look-at-my/recycler/internal/bin"

// Backend answers the FreeDesktop bin, which is this host's own.
func Backend() (bin.Backend, error) { return fdoBackend() }
