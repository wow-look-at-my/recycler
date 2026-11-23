//go:build !unix && !windows

// Install has no service manager to register with on a platform with no recycle bin.

package daemon

import "github.com/wow-look-at-my/recycler/internal/bin"

// Install has no service manager to register with on a platform with no recycle bin.
func Install(string) (string, error) { return "", bin.ErrUnsupported }
