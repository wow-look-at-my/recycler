//go:build !linux || cosmo

// Package workspace serves a directory through FUSE so that a full filesystem never reaches the
// program writing to it. FUSE is a Linux interface, so every other platform gets this stub.
package workspace

import "errors"

// ErrUnsupported reports that this platform serves no workspace.
var ErrUnsupported = errors.New("recycler: a workspace mount needs FUSE, which this platform has not")

// Options configure a mount. They are accepted everywhere so a caller compiles on every platform.
type Options struct {
	Reclaim func(atLeast uint64) (uint64, error)
	Report  func(freed uint64, err error)
	Debug   bool
}

// Mount always fails here.
func Mount(string, string, Options) (any, error) { return nil, ErrUnsupported }
