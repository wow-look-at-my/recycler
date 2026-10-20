//go:build !cgo && (darwin || cosmo)

package trash

// The trash on macOS is an operation the system performs, reached through
// Foundation.

import (
	"path/filepath"

	"github.com/wow-look-at-my/recycler/internal/fsutil"
)

// trashItem moves path into dir and answers where it landed.
func trashItem(path, dir string) (string, error) {
	dest := filepath.Join(dir, fsutil.UniqueName(filepath.Base(path), dir))
	if err := fsutil.Move(path, dest); err != nil {
		return "", err
	}
	return dest, nil
}
