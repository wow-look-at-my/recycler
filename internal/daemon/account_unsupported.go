//go:build !unix && !windows

package daemon

import "github.com/wow-look-at-my/recycler/internal/bin"

func accountHome() (string, error) { return "", bin.ErrUnsupported }

func accountCacheDir() (string, error) { return "", bin.ErrUnsupported }

func UseAccountEnvironment() error { return bin.ErrUnsupported }
