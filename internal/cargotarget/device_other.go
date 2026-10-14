//go:build !unix

package cargotarget

import "io/fs"

// deviceOf reports that this platform answers no filesystem number to compare.
func deviceOf(fs.FileInfo) (uint64, bool) { return 0, false }
