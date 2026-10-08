package trash

import (
	"os"
	"path/filepath"
	"strconv"
)

// volumeTrashes returns the existing <mount>/.Trashes/<uid> directory of each mount point, in
// order and without repeats. A mount point whose trash does not exist yet contributes nothing.
func volumeTrashes(mounts []string, uid int) []string {
	var dirs []string
	seen := map[string]bool{}
	for _, mount := range mounts {
		candidate := filepath.Join(mount, ".Trashes", strconv.Itoa(uid))
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			dirs = append(dirs, candidate)
		}
	}
	return dirs
}
