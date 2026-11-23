//go:build !unix

// SameFilesystem reports whether a and b sit on one volume, where a rename between them moves no data and needs no free space.

package fsutil

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// SameFilesystem reports whether a and b sit on one volume, where a rename
// between them moves no data and needs no free space.
func SameFilesystem(a, b string) (bool, error) {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b)), nil
}

// Allocated is the space an entry takes on its filesystem. Without st_blocks
// that is the length it states.
func Allocated(info fs.FileInfo) int64 {
	return info.Size()
}
