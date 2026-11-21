//go:build linux || freebsd || netbsd || openbsd || dragonfly || solaris

// Entries in a FreeDesktop bin's files/ that have no .trashinfo record.

package trash

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/go-containers/set"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
)

// adoptUnrecorded lists each entry in files/ that has no .trashinfo. Without a
// record the daemon cannot see the entry, so its bytes are never given back.
// This writes a record with no Path, so a restore asks for a destination.
func (t *fdoTrash) adoptUnrecorded(dir string, recorded set.Set[string]) []bin.Item {
	entries, err := os.ReadDir(filepath.Join(dir, trashFilesDir))
	if err != nil {
		return nil
	}
	var items []bin.Item
	for _, entry := range entries {
		name := entry.Name()
		if recorded.Contains(name) {
			continue
		}
		if it, ok := adoptOne(dir, name); ok {
			items = append(items, it)
		}
	}
	return items
}

// adoptOne writes a record for the entry name in files/, which has none. It
// answers false when the entry is gone, or a recycle claimed the name first.
func adoptOne(dir, name string) (bin.Item, bool) {
	file := filepath.Join(dir, trashFilesDir, name)
	st, err := os.Lstat(file)
	if err != nil {
		return bin.Item{}, false
	}
	size := fsutil.TreeSize(file)
	f, err := os.OpenFile(filepath.Join(dir, trashInfoDir, name+trashInfoExt),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	switch {
	case errors.Is(err, fs.ErrExist):
		// A recycle claimed the name after the read of info/.
		return bin.Item{}, false
	case err == nil:
		_, err = fmt.Fprintf(f, "[Trash Info]\nPath=\nDeletionDate=%s\nSize=%d\n",
			st.ModTime().Format(deletionDateLayout), size)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(f.Name())
		}
	}
	// A full disk refuses the record. The size is still listed, because a full
	// disk is the case that needs this entry evicted.
	return bin.Item{
		ID:        file,
		Name:      name,
		DeletedAt: st.ModTime(),
		Size:      size,
		IsDir:     st.IsDir(),
	}, true
}
