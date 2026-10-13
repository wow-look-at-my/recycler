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
