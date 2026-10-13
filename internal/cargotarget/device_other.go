//go:build !unix

package cargotarget

import "io/fs"

// deviceOf reports that this platform answers no filesystem number to compare.
func deviceOf(fs.FileInfo) (uint64, bool) { return 0, false }

// blocksOf reports no allocation for this platform, so sizes fall back to the length each file states.
func blocksOf(fs.FileInfo) (int64, bool) { return 0, false }
