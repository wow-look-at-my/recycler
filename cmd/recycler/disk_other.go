//go:build !unix

// disk_other.go holds diskOf for systems that name a volume in the path.

package main

import "path/filepath"

// diskOf names the volume dir sits on, such as C:\.
func diskOf(dir string) string {
	if vol := filepath.VolumeName(dir); vol != "" {
		return vol + `\`
	}
	return dir
}
