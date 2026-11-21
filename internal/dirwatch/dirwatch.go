// Package dirwatch reports changes to the entries of watched directories.

package dirwatch

import "errors"

// ErrUnsupported is what New answers on a system with no directory watch.
var ErrUnsupported = errors.New("dirwatch: this system has no directory watch")

// An Event names one entry that changed in a watched directory.
type Event struct {
	Dir  string
	Name string
}

// A Batch is what one Read drained.
type Batch struct {
	Events []Event

	// Overflow is true when the kernel dropped events. Every watched directory must then be read again in full.
	Overflow bool
}
