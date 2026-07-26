//go:build darwin

package recycler

// macOS Trash implementation.
//
// Finder moves deleted files into ~/.Trash (or .Trashes/$uid on other volumes)
// and remembers where each one came from in its own private metadata, which is
// not a documented or writable format. This implementation therefore keeps its
// own index of original locations alongside the trash. Items recycled by Finder
// or by other tools still show up in List, but without an original location, so
// restoring them needs an explicit destination.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const indexVersion = 1

type macTrash struct {
	home  string // ~/.Trash
	index string // path of this package's index of original locations
	uid   int
}

func platformBackend() (backend, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("recycler: locating home directory: %w", err)
	}
	return &macTrash{
		home:  filepath.Join(home, ".Trash"),
		index: filepath.Join(home, "Library", "Application Support", "recycler", "index.json"),
		uid:   os.Getuid(),
	}, nil
}

// trashIndex records where recycled files came from, keyed by the absolute path
// of the file inside the trash.
type trashIndex struct {
	Version int                   `json:"version"`
	Entries map[string]indexEntry `json:"entries"`
}

type indexEntry struct {
	OriginalPath string    `json:"original_path"`
	DeletedAt    time.Time `json:"deleted_at"`
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
	dest := filepath.Join(dir, uniqueName(filepath.Base(abs), dir))
	if err := move(abs, dest); err != nil {
		return err
	}
	return t.updateIndex(func(idx *trashIndex) error {
		idx.Entries[dest] = indexEntry{OriginalPath: abs, DeletedAt: time.Now()}
		return nil
	})
}

func (t *macTrash) list() ([]Item, error) {
	idx, err := t.loadIndex()
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, dir := range t.trashDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Name() == ".DS_Store" {
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
				DeletedAt: info.ModTime(),
				Size:      treeSize(file),
				IsDir:     info.IsDir(),
			}
			if recorded, ok := idx.Entries[file]; ok {
				item.OriginalPath = recorded.OriginalPath
				item.Name = filepath.Base(recorded.OriginalPath)
				if !recorded.DeletedAt.IsZero() {
					item.DeletedAt = recorded.DeletedAt
				}
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
	if dest == "" {
		idx, err := t.loadIndex()
		if err != nil {
			return "", err
		}
		recorded, ok := idx.Entries[file]
		if !ok || recorded.OriginalPath == "" {
			return "", fmt.Errorf("%w: %s (recycled outside this tool; restore it with an explicit destination)", ErrUnknownOrigin, id)
		}
		dest = recorded.OriginalPath
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
	return dest, t.updateIndex(func(idx *trashIndex) error {
		delete(idx.Entries, file)
		return nil
	})
}

func (t *macTrash) purge(ids []string) error {
	var errs []error
	removed := make([]string, 0, len(ids))
	for _, id := range ids {
		file, err := t.resolveID(id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.RemoveAll(file); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, file)
	}
	if len(removed) > 0 {
		if err := t.updateIndex(func(idx *trashIndex) error {
			for _, file := range removed {
				delete(idx.Entries, file)
			}
			return nil
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *macTrash) empty() error {
	var errs []error
	for _, dir := range t.trashDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := t.updateIndex(func(idx *trashIndex) error {
		idx.Entries = map[string]indexEntry{}
		return nil
	}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
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
	if !known {
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

func (t *macTrash) loadIndex() (*trashIndex, error) {
	idx := &trashIndex{Version: indexVersion, Entries: map[string]indexEntry{}}
	data, err := os.ReadFile(t.index)
	if errors.Is(err, fs.ErrNotExist) {
		return idx, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return idx, nil
	}
	if err := json.Unmarshal(data, idx); err != nil {
		// A damaged index costs restore information, not data: carry on with
		// an empty one rather than making the whole recycle bin unusable.
		return &trashIndex{Version: indexVersion, Entries: map[string]indexEntry{}}, nil
	}
	if idx.Entries == nil {
		idx.Entries = map[string]indexEntry{}
	}
	return idx, nil
}

// updateIndex applies fn to the index under an exclusive lock, then writes it
// back, dropping entries whose files are gone from a trash directory that is
// currently reachable.
func (t *macTrash) updateIndex(fn func(*trashIndex) error) error {
	if err := os.MkdirAll(filepath.Dir(t.index), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(t.index, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	idx := &trashIndex{Version: indexVersion, Entries: map[string]indexEntry{}}
	if data, err := io.ReadAll(f); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, idx); err != nil || idx.Entries == nil {
			idx = &trashIndex{Version: indexVersion, Entries: map[string]indexEntry{}}
		}
	}
	if err := fn(idx); err != nil {
		return err
	}
	t.prune(idx)

	idx.Version = indexVersion
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt(append(data, '\n'), 0); err != nil {
		return err
	}
	return f.Sync()
}

// prune drops index entries for files that no longer exist, but only when their
// trash directory is reachable - an unmounted volume must not cost the items on
// it their original locations.
func (t *macTrash) prune(idx *trashIndex) {
	for file := range idx.Entries {
		dir := filepath.Dir(file)
		if !strings.HasPrefix(file, dir+string(filepath.Separator)) {
			delete(idx.Entries, file)
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			continue // trash directory unreachable: keep the entry
		}
		if _, err := os.Lstat(file); errors.Is(err, fs.ErrNotExist) {
			delete(idx.Entries, file)
		}
	}
}
