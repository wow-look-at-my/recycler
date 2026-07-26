//go:build linux || freebsd || netbsd || openbsd || dragonfly || solaris

package recycler

// FreeDesktop.org Trash implementation, following the Trash specification v1.0:
// https://specifications.freedesktop.org/trash-spec/trashspec-1.0.html
//
// Files recycled from the filesystem holding the home directory go to the home
// trash ($XDG_DATA_HOME/Trash). Files on any other filesystem go to a trash
// directory on that filesystem, so recycling never has to copy data across
// filesystems.

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	trashFilesDir = "files"
	trashInfoDir  = "info"
	trashInfoExt  = ".trashinfo"
	// deletionDateLayout is the local-time layout the spec mandates for the
	// DeletionDate field.
	deletionDateLayout = "2006-01-02T15:04:05"
)

type fdoTrash struct {
	home string // home trash directory ($XDG_DATA_HOME/Trash)
	uid  int
}

func platformBackend() (backend, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" || !filepath.IsAbs(dataHome) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("recycler: locating home directory: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return &fdoTrash{home: filepath.Join(dataHome, "Trash"), uid: os.Getuid()}, nil
}

// trashInfo is the parsed content of a .trashinfo file.
type trashInfo struct {
	origPath  string // as written in the file, possibly relative to a top directory
	deletedAt time.Time
}

func (t *fdoTrash) recycle(paths []string) error {
	var errs []error
	for _, path := range paths {
		if err := t.recycleOne(path); err != nil {
			errs = append(errs, fmt.Errorf("recycling %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

func (t *fdoTrash) recycleOne(path string) error {
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

	dir, top, err := t.trashDirFor(abs)
	if err != nil {
		return err
	}

	// Record the original location relative to the top directory when using a
	// per-filesystem trash, so the entry survives the filesystem being mounted
	// somewhere else.
	recorded := abs
	if top != "" {
		if rel, err := filepath.Rel(top, abs); err == nil {
			recorded = rel
		}
	}

	name, info, err := createInfoFile(dir, filepath.Base(abs))
	if err != nil {
		return err
	}
	infoPath := filepath.Join(dir, trashInfoDir, name+trashInfoExt)
	_, err = fmt.Fprintf(info, "[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		escapePath(recorded), time.Now().Format(deletionDateLayout))
	if closeErr := info.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(infoPath)
		return err
	}

	if err := move(abs, filepath.Join(dir, trashFilesDir, name)); err != nil {
		os.Remove(infoPath)
		return err
	}
	return nil
}

func (t *fdoTrash) list() ([]Item, error) {
	var items []Item
	for _, dir := range t.trashDirs() {
		entries, err := os.ReadDir(filepath.Join(dir, trashInfoDir))
		if err != nil {
			continue // unreadable or absent trash directories are skipped
		}
		top := ""
		if dir != t.home {
			top = topDirOfTrash(dir)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), trashInfoExt) {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), trashInfoExt)
			file := filepath.Join(dir, trashFilesDir, name)
			st, err := os.Lstat(file)
			if err != nil {
				continue // orphaned .trashinfo with no matching file
			}
			info, err := readInfoFile(filepath.Join(dir, trashInfoDir, entry.Name()))
			if err != nil {
				continue
			}
			orig := info.origPath
			if !filepath.IsAbs(orig) && top != "" {
				orig = filepath.Join(top, orig)
			}
			deletedAt := info.deletedAt
			if deletedAt.IsZero() {
				deletedAt = st.ModTime()
			}
			items = append(items, Item{
				ID:           file,
				Name:         filepath.Base(orig),
				OriginalPath: orig,
				DeletedAt:    deletedAt,
				Size:         treeSize(file),
				IsDir:        st.IsDir(),
			})
		}
	}
	sortItems(items)
	return items, nil
}

func (t *fdoTrash) restore(id, dest string) (string, error) {
	file, infoPath, err := t.resolveID(id)
	if err != nil {
		return "", err
	}
	if dest == "" {
		info, err := readInfoFile(infoPath)
		if err != nil {
			return "", err
		}
		dest = info.origPath
		if dest == "" {
			return "", fmt.Errorf("%w: %s", ErrUnknownOrigin, id)
		}
		if !filepath.IsAbs(dest) {
			top := topDirOfTrash(filepath.Dir(filepath.Dir(file)))
			if top == "" {
				return "", fmt.Errorf("%w: %s", ErrUnknownOrigin, id)
			}
			dest = filepath.Join(top, dest)
		}
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
	os.Remove(infoPath)
	return dest, nil
}

func (t *fdoTrash) purge(ids []string) error {
	var errs []error
	for _, id := range ids {
		file, infoPath, err := t.resolveID(id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.RemoveAll(file); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Remove(infoPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *fdoTrash) empty() error {
	var errs []error
	for _, dir := range t.trashDirs() {
		for _, sub := range []string{trashFilesDir, trashInfoDir} {
			entries, err := os.ReadDir(filepath.Join(dir, sub))
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if err := os.RemoveAll(filepath.Join(dir, sub, entry.Name())); err != nil {
					errs = append(errs, err)
				}
			}
		}
		// The cached directory sizes are only valid for entries that still
		// exist, so drop the cache along with them.
		os.Remove(filepath.Join(dir, "directorysizes"))
	}
	return errors.Join(errs...)
}

// resolveID validates an item ID and returns the path of the recycled file and
// of its .trashinfo file. IDs that do not name a file inside a trash directory
// belonging to this user are rejected, so a malformed ID can never delete or
// move something outside the recycle bin.
func (t *fdoTrash) resolveID(id string) (file, infoPath string, err error) {
	clean := filepath.Clean(id)
	parent := filepath.Dir(clean)
	if filepath.Base(parent) != trashFilesDir {
		return "", "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	dir := filepath.Dir(parent)
	known := false
	for _, candidate := range t.trashDirs() {
		if candidate == dir {
			known = true
			break
		}
	}
	if !known {
		return "", "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if _, err := os.Lstat(clean); err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return clean, filepath.Join(dir, trashInfoDir, filepath.Base(clean)+trashInfoExt), nil
}

// trashDirFor returns the trash directory a path should be recycled into,
// creating it if needed. top is the top directory of the filesystem when the
// trash is a per-filesystem one, and empty for the home trash.
func (t *fdoTrash) trashDirFor(path string) (dir, top string, err error) {
	if err := ensureTrashDir(t.home); err != nil {
		return "", "", err
	}
	homeDev, err := deviceOf(t.home)
	if err != nil {
		return "", "", err
	}
	srcDev, err := deviceOf(filepath.Dir(path))
	if err != nil {
		return "", "", err
	}
	if srcDev == homeDev {
		return t.home, "", nil
	}

	top = topDirOf(filepath.Dir(path), srcDev)
	// Preferred location: an administrator-created $topdir/.Trash with the
	// sticky bit set, in which each user gets a subdirectory.
	if shared := filepath.Join(top, ".Trash"); isStickyDir(shared) {
		dir = filepath.Join(shared, strconv.Itoa(t.uid))
		if err := ensureTrashDir(dir); err == nil {
			return dir, top, nil
		}
	}
	dir = filepath.Join(top, ".Trash-"+strconv.Itoa(t.uid))
	if err := ensureTrashDir(dir); err != nil {
		return "", "", err
	}
	return dir, top, nil
}

// trashDirs returns every trash directory belonging to this user that exists
// right now: the home trash plus one per mounted filesystem.
func (t *fdoTrash) trashDirs() []string {
	dirs := []string{t.home}
	seen := map[string]bool{t.home: true}
	for _, top := range mountPoints() {
		for _, candidate := range []string{
			filepath.Join(top, ".Trash", strconv.Itoa(t.uid)),
			filepath.Join(top, ".Trash-"+strconv.Itoa(t.uid)),
		} {
			if seen[candidate] {
				continue
			}
			if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
				seen[candidate] = true
				dirs = append(dirs, candidate)
			}
		}
	}
	return dirs
}

// topDirOfTrash returns the filesystem top directory a per-filesystem trash
// directory belongs to, or "" if dir is not one.
func topDirOfTrash(dir string) string {
	base := filepath.Base(dir)
	switch {
	case strings.HasPrefix(base, ".Trash-"):
		return filepath.Dir(dir)
	default:
		// $topdir/.Trash/$uid
		if filepath.Base(filepath.Dir(dir)) == ".Trash" {
			return filepath.Dir(filepath.Dir(dir))
		}
	}
	return ""
}

func ensureTrashDir(dir string) error {
	for _, d := range []string{dir, filepath.Join(dir, trashFilesDir), filepath.Join(dir, trashInfoDir)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func isStickyDir(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 && fi.Mode()&os.ModeSticky != 0
}

// createInfoFile atomically claims a free name in the trash directory by
// creating its .trashinfo file, and returns the name along with the open file.
func createInfoFile(dir, want string) (string, *os.File, error) {
	if want == "" || want == "." || want == ".." || strings.ContainsRune(want, filepath.Separator) {
		want = "recycled"
	}
	ext := filepath.Ext(want)
	base := strings.TrimSuffix(want, ext)
	for n := 0; n < 1<<16; n++ {
		name := want
		if n > 0 {
			name = fmt.Sprintf("%s_%d%s", base, n, ext)
		}
		if _, err := os.Lstat(filepath.Join(dir, trashFilesDir, name)); err == nil {
			continue // the data file name is taken, even if the info name is free
		}
		f, err := os.OpenFile(filepath.Join(dir, trashInfoDir, name+trashInfoExt),
			os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return name, f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("recycler: no free name for %s in %s", want, dir)
}

func readInfoFile(path string) (trashInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return trashInfo{}, err
	}
	defer f.Close()

	var info trashInfo
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Path":
			if decoded, err := url.PathUnescape(value); err == nil {
				info.origPath = decoded
			} else {
				info.origPath = value
			}
		case "DeletionDate":
			info.deletedAt = parseDeletionDate(strings.TrimSpace(value))
		}
	}
	if err := scanner.Err(); err != nil {
		return trashInfo{}, err
	}
	return info, nil
}

func parseDeletionDate(value string) time.Time {
	for _, layout := range []string{deletionDateLayout, time.RFC3339} {
		if ts, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return ts
		}
	}
	return time.Time{}
}

// escapePath percent-encodes a path for the Path field of a .trashinfo file,
// leaving the separators intact as the specification requires.
func escapePath(path string) string {
	u := url.URL{Path: path}
	return u.EscapedPath()
}
