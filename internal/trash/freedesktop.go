//go:build linux || freebsd || netbsd || openbsd || dragonfly || solaris

package trash

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

	"github.com/wow-look-at-my/go-containers/set"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
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

func Backend() (bin.Backend, error) {
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
	size      int64 // bin.SizeUnknown when the file records none
}

func (t *fdoTrash) Recycle(paths []string) error {
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
	// The size is measured here, while the item is still at its original
	// location, and recorded. This is the only walk of its contents there ever
	// is: what is in the bin does not change, so every later reader takes the
	// recorded number instead of walking the tree again.
	infoPath := filepath.Join(dir, trashInfoDir, name+trashInfoExt)
	_, err = fmt.Fprintf(info, "[Trash Info]\nPath=%s\nDeletionDate=%s\nSize=%d\n",
		escapePath(recorded), time.Now().Format(deletionDateLayout), fsutil.TreeSize(abs))
	if closeErr := info.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(infoPath)
		return err
	}

	if err := fsutil.Move(abs, filepath.Join(dir, trashFilesDir, name)); err != nil {
		os.Remove(infoPath)
		return err
	}
	return nil
}

func (t *fdoTrash) List() ([]bin.Item, error) {
	var items []bin.Item
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
			size := info.size
			if size == bin.SizeUnknown {
				// An entry another implementation wrote records no size.
				// Measure it once and record it, so this is the last walk
				// of it: listing is on the daemon's poll path, and a bin
				// full of foreign entries would otherwise be re-walked
				// every time anything asks what is in it.
				size = fsutil.TreeSize(file)
				recordSize(filepath.Join(dir, trashInfoDir, entry.Name()), size)
			}
			items = append(items, bin.Item{
				ID:           file,
				Name:         filepath.Base(orig),
				OriginalPath: orig,
				DeletedAt:    deletedAt,
				Size:         size,
				IsDir:        st.IsDir(),
			})
		}
	}
	bin.Sort(items)
	return items, nil
}

func (t *fdoTrash) Restore(id, dest string) (string, error) {
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
			return "", fmt.Errorf("%w: %s", bin.ErrUnknownOrigin, id)
		}
		if !filepath.IsAbs(dest) {
			top := topDirOfTrash(filepath.Dir(filepath.Dir(file)))
			if top == "" {
				return "", fmt.Errorf("%w: %s", bin.ErrUnknownOrigin, id)
			}
			dest = filepath.Join(top, dest)
		}
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
	os.Remove(infoPath)
	return dest, nil
}

// evict destroys a recycled item and its metadata. Only the disk-pressure
// daemon calls this. resolveID does the same validation restore relies on, so
// an ID that does not name an entry in this user's bin removes nothing.
func (t *fdoTrash) Evict(id string) error {
	file, infoPath, err := t.resolveID(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(file); err != nil {
		return err
	}
	// The data is gone; a leftover .trashinfo would list as an entry whose
	// file is missing. list already skips those, so a failure to remove it
	// is untidy rather than wrong.
	os.Remove(infoPath)
	return nil
}

// resolveID validates an item ID and returns the path of the recycled file and
// of its .trashinfo file. IDs that do not name a file inside a trash directory
// belonging to this user are rejected, so a malformed ID can never delete or
// move something outside the recycle bin.
func (t *fdoTrash) resolveID(id string) (file, infoPath string, err error) {
	clean := filepath.Clean(id)
	parent := filepath.Dir(clean)
	if filepath.Base(parent) != trashFilesDir {
		return "", "", fmt.Errorf("%w: %s", bin.ErrNotFound, id)
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
		return "", "", fmt.Errorf("%w: %s", bin.ErrNotFound, id)
	}
	if _, err := os.Lstat(clean); err != nil {
		return "", "", fmt.Errorf("%w: %s", bin.ErrNotFound, id)
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
	homeDev, err := fsutil.DeviceOf(t.home)
	if err != nil {
		return "", "", err
	}
	srcDev, err := fsutil.DeviceOf(filepath.Dir(path))
	if err != nil {
		return "", "", err
	}
	if srcDev == homeDev {
		return t.home, "", nil
	}

	top = fsutil.TopDirOf(filepath.Dir(path), srcDev)
	// Preferred location: an administrator-created $topdir/.Trash with the
	// sticky bit set, in which each user gets a subdirectory.
	if shared := filepath.Join(top, ".Trash"); fsutil.IsStickyDir(shared) {
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
	seen := set.Of[string](t.home)
	for _, top := range mountPoints() {
		for _, candidate := range []string{
			filepath.Join(top, ".Trash", strconv.Itoa(t.uid)),
			filepath.Join(top, ".Trash-"+strconv.Itoa(t.uid)),
		} {
			if seen.Contains(candidate) {
				continue
			}
			if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
				seen.Add(candidate)
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

	info := trashInfo{size: bin.SizeUnknown}
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
		case "Size":
			if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				info.size = n
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return trashInfo{}, err
	}
	return info, nil
}

// recordSize appends a Size line to a .trashinfo file that has none, so the
// tree behind it is never walked a second time. The size of something in the
// bin does not change, which is what makes one measurement enough.
//
// Best effort: a bin on a read-only mount, or one owned by another user, keeps
// working and pays for a walk each time it is listed. Nothing but that walk is
// lost, so a failure here is not worth failing a listing over.
func recordSize(infoPath string, size int64) {
	if size == bin.SizeUnknown {
		return
	}
	f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "Size=%d\n", size)
	f.Close()
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
