//go:build darwin

package trash

import (
	"path/filepath"
	"syscall"
)

// mountPoints returns every mounted filesystem's mount point, plus the
// entries of /Volumes.
func mountPoints() []string {
	points := systemMountPoints()
	if volumes, err := filepath.Glob("/Volumes/*"); err == nil {
		points = append(points, volumes...)
	}
	return points
}

// systemMountPoints reads the kernel's mount table.
func systemMountPoints() []string {
	count, err := syscall.Getfsstat(nil, 2) // MNT_NOWAIT
	if err != nil || count <= 0 {
		return nil
	}
	// A volume can mount between both calls, so Getfsstat fills at most len(buf).
	buf := make([]syscall.Statfs_t, count+8)
	count, err = syscall.Getfsstat(buf, 2) // MNT_NOWAIT
	if err != nil {
		return nil
	}
	points := make([]string, 0, count)
	for i := 0; i < count; i++ {
		points = append(points, mountName(buf[i].Mntonname[:]))
	}
	return points
}

// mountName converts a NUL-terminated C string to a Go string.
func mountName(raw []int8) string {
	out := make([]byte, 0, len(raw))
	for _, c := range raw {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}
