//go:build darwin

package trash

// macOS Trash implementation.
//
// Finder moves deleted files into ~/.Trash (or .Trashes/$uid on other volumes)
// and records where each item came from in that trash directory's own .DS_Store,
// as the "ptbL" and "ptbN" records behind its Put Back command. Apple documents
// neither the file nor an API for reading it, but it is the only place the
// original location exists, so this reads and writes those same records
// (internal/putback over internal/dsstore) instead of keeping an index of its
// own. Items recycled here can therefore be put back from Finder, and items
// Finder trashed can be restored here.

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

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/dsstore"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
	"github.com/wow-look-at-my/recycler/internal/putback"
)

type macTrash struct {
	home string // ~/.Trash
	uid  int
}

func Backend() (bin.Backend, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("recycler: locating home directory: %w", err)
	}
	return &macTrash{home: filepath.Join(home, ".Trash"), uid: os.Getuid()}, nil
}

func (t *macTrash) Recycle(paths []string) error {
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
	dest := filepath.Join(dir, putback.TrashName(filepath.Base(abs), dir))
	if err := fsutil.Move(abs, dest); err != nil {
		return err
	}
	if err := putback.Set(dir, filepath.Base(dest), putback.Of(t.volumeRoot(dir), abs)); err != nil {
		return fmt.Errorf("moved to the trash, but its original location could not be recorded: %w", err)
	}
	return nil
}

func (t *macTrash) List() ([]bin.Item, error) {
	var items []bin.Item
	for _, dir := range t.trashDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		origins := putback.Read(dir)
		root := t.volumeRoot(dir)
		for _, entry := range entries {
			if entry.Name() == dsstore.FileName {
				continue
			}
			file := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}
			item := bin.Item{
				ID:        file,
				Name:      entry.Name(),
				DeletedAt: deletedAt(info),
				Size:      fsutil.TreeSize(file),
				IsDir:     info.IsDir(),
			}
			if origin, ok := origins[entry.Name()]; ok {
				item.OriginalPath = dataVolumePath(origin.Path(root))
				item.Name = origin.Name
			}
			items = append(items, item)
		}
	}
	bin.Sort(items)
	return items, nil
}

func (t *macTrash) Restore(id, dest string) (string, error) {
	file, err := t.resolveID(id)
	if err != nil {
		return "", err
	}
	dir, name := filepath.Dir(file), filepath.Base(file)
	if dest == "" {
		origin, ok := putback.Read(dir)[name]
		if !ok {
			return "", fmt.Errorf("%w: %s (nothing recorded where it came from; restore it with an explicit destination)", bin.ErrUnknownOrigin, id)
		}
		dest = dataVolumePath(origin.Path(t.volumeRoot(dir)))
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if err := fsutil.PrepareDest(dest); err != nil {
		return "", err
	}
	if err := fsutil.Move(file, dest); err != nil {
		return "", err
	}
	return dest, putback.Clear(dir, name)
}

// evict destroys a trashed item.
func (t *macTrash) Evict(id string) error {
	file, err := t.resolveID(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(file)
}

// resolveID validates an item ID, rejecting anything.
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
	if !known || filepath.Base(clean) == dsstore.FileName {
		return "", fmt.Errorf("%w: %s", bin.ErrNotFound, id)
	}
	if _, err := os.Lstat(clean); err != nil {
		return "", fmt.Errorf("%w: %s", bin.ErrNotFound, id)
	}
	return clean, nil
}

// trashDirFor returns the trash directory a path should be recycled into.
func (t *macTrash) trashDirFor(path string) (string, error) {
	if err := os.MkdirAll(t.home, 0o700); err != nil {
		return "", err
	}
	homeDev, err := fsutil.DeviceOf(t.home)
	if err != nil {
		return "", err
	}
	srcDev, err := fsutil.DeviceOf(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	if srcDev == homeDev {
		return t.home, nil
	}

	top := fsutil.TopDirOf(filepath.Dir(path), srcDev)
	dir := filepath.Join(top, ".Trashes", strconv.Itoa(t.uid))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("recycler: no usable trash directory on %s: %w", top, err)
	}
	return dir, nil
}

// trashDirs returns every trash directory belonging to this user.
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

// volumeRoot returns the mount point a trash directory's put back locations sit under.
func (t *macTrash) volumeRoot(dir string) string {
	if dir == t.home {
		return "/"
	}
	return filepath.Dir(filepath.Dir(dir))
}

// dataVolumePath rewrites a location Finder recorded through the data volume's own mount.
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

// deletedAt reports when an item was moved to the trash.
func deletedAt(info fs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctimespec.Unix())
	}
	return info.ModTime()
}
