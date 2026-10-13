//go:build unix

package cargotarget

import (
	"io/fs"
	"syscall"
)

// deviceOf returns the number of the filesystem an entry sits on.
func deviceOf(info fs.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

// blocksOf returns the bytes a file occupies on its filesystem, in st_blocks
// units of many bytes.
func blocksOf(info fs.FileInfo) (int64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int64(st.Blocks) * 512, true
}
