//go:build darwin

package recycler

// macOS Trash implementation.
//
// Finder moves deleted files into ~/.Trash (or .Trashes/$uid on other volumes)
// and records where each one came from in that trash directory's own .DS_Store,
// as the "ptbL" and "ptbN" records behind its Put Back command. Apple documents
// neither the file nor an API for reading it, but it is the only place the
// original location exists, so this package reads and writes those same records
// (see putback.go and dsstore.go) instead of keeping an index of its own.
// Items recycled here can therefore be put back from Finder, and items Finder
// trashed can be restored here.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type macTrash struct {
	home string // ~/.Trash
	uid  int
}

func platformBackend() (backend, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("recycler: locating home directory: %w", err)
	}
	return &macTrash{home: filepath.Join(home, ".Trash"), uid: os.Getuid()}, nil
}

func (t *macTrash) recycle(paths []string) error {
	var errs []error
	for _, path := range paths {
		if err := t.recycleOne(path); err != nil {
			errs = append(errs, fmt.Errorf("recycling %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

func (t *macTrash) recycleOne(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err != nil {
		return err
	}
	if abs == filepath.Clean("/") {
		return errors.New("refusing to recycle the filesystem root")
	}

	dir, err := t.trashDirFor(abs)
	if err != nil {
		return err
	}
	dest := filepath.Join(dir, trashName(filepath.Base(abs), dir))
	if err := move(abs, dest); err != nil {
		return err
	}
	if err := setPutBack(dir, filepath.Base(dest), putBackOf(t.volumeRoot(dir), abs)); err != nil {
		return fmt.Errorf("moved to the trash, but its original location could not be recorded: %w", err)
	}
	return nil
}

func (t *macTrash) list() ([]Item, error) {
	var items []Item
	for _, dir := range t.trashDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		origins := readPutBacks(dir)
		root := t.volumeRoot(dir)
		for _, entry := range entries {
			if entry.Name() == dsStoreName {
				continue
			}
			file := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}
			item := Item{
				ID:        file,
				Name:      entry.Name(),
				DeletedAt: deletedAt(info),
				Size:      treeSize(file),
				IsDir:     info.IsDir(),
			}
			if origin, ok := origins[entry.Name()]; ok {
				item.OriginalPath = dataVolumePath(origin.path(root))
				item.Name = origin.Name
			}
			items = append(items, item)
		}
	}
	sortItems(items)
	return items, nil
}

func (t *macTrash) restore(id, dest string) (string, error) {
	file, err := t.resolveID(id)
	if err != nil {
		return "", err
	}
	dir, name := filepath.Dir(file), filepath.Base(file)
	if dest == "" {
		origin, ok := readPutBacks(dir)[name]
		if !ok {
			return "", fmt.Errorf("%w: %s (nothing recorded where it came from; restore it with an explicit destination)", ErrUnknownOrigin, id)
		}
		dest = dataVolumePath(origin.path(t.volumeRoot(dir)))
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if err := prepareDest(dest); err != nil {
		return "", err
	}
	if err := move(file, dest); err != nil {
		return "", err
	}
	return dest, clearPutBack(dir, name)
}

// evict destroys a trashed item. Only the disk-pressure daemon calls this.
//
// The item's put back records are left in the trash directory's .DS_Store.
// They are keyed by a name that is now free, Finder ignores a record whose
// item is gone, and rewriting the whole file under its lock to drop two
// strings would put the display settings beside them at risk for nothing.
func (t *macTrash) evict(id string) error {
	file, err := t.resolveID(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(file)
}

// resolveID validates an item ID, rejecting anything that does not name an
// existing entry directly inside one of this user's trash directories.
func (t *macTrash) resolveID(id string) (string, error) {
	clean := filepath.Clean(id)
	dir := filepath.Dir(clean)
	known := false
	for _, candidate := range t.trashDirs() {
		if candidate == dir {
			known = true
			break
		}
	}
	if !known || filepath.Base(clean) == dsStoreName {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if _, err := os.Lstat(clean); err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return clean, nil
}

// trashDirFor returns the trash directory a path should be recycled into,
// creating it if needed. Files outside the home volume go to that volume's
// .Trashes directory so recycling stays a rename.
func (t *macTrash) trashDirFor(path string) (string, error) {
	if err := os.MkdirAll(t.home, 0o700); err != nil {
		return "", err
	}
	homeDev, err := deviceOf(t.home)
	if err != nil {
		return "", err
	}
	srcDev, err := deviceOf(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	if srcDev == homeDev {
		return t.home, nil
	}

	top := topDirOf(filepath.Dir(path), srcDev)
	dir := filepath.Join(top, ".Trashes", strconv.Itoa(t.uid))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("recycler: no usable trash directory on %s: %w", top, err)
	}
	return dir, nil
}

// trashDirs returns every trash directory belonging to this user that exists
// right now: the home trash plus one per mounted volume.
func (t *macTrash) trashDirs() []string {
	dirs := []string{t.home}
	seen := map[string]bool{t.home: true}
	volumes, err := filepath.Glob("/Volumes/*")
	if err != nil {
		return dirs
	}
	for _, volume := range volumes {
		candidate := filepath.Join(volume, ".Trashes", strconv.Itoa(t.uid))
		if seen[candidate] {
			continue
		}
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			seen[candidate] = true
			dirs = append(dirs, candidate)
		}
	}
	return dirs
}

// volumeRoot returns the mount point that a trash directory's put back
// locations are relative to: the volume holding a .Trashes directory, and the
// root for the home trash.
func (t *macTrash) volumeRoot(dir string) string {
	if dir == t.home {
		return "/"
	}
	return filepath.Dir(filepath.Dir(dir))
}

// dataVolumePath rewrites a location Finder recorded through the data volume's
// own mount point - /System/Volumes/Data/Users/me/notes.txt - as the
// /Users/me/notes.txt that firmlinks make the very same file. It only does so
// when that shorter path's directory really is there, so an unusual layout
// keeps the location exactly as recorded.
func dataVolumePath(path string) string {
	rest, ok := strings.CutPrefix(path, "/System/Volumes/Data/")
	if !ok {
		return path
	}
	short := "/" + rest
	if _, err := os.Stat(filepath.Dir(short)); err != nil {
		return path
	}
	return short
}

// deletedAt reports when an item was moved to the trash. macOS records no
// deletion time anywhere, but moving a file to the trash is a rename, which
// updates its inode change time.
func deletedAt(info fs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctimespec.Unix())
	}
	return info.ModTime()
}
