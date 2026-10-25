//go:build linux || freebsd || netbsd || openbsd || dragonfly || solaris

package trash

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
	"github.com/wow-look-at-my/recycler/internal/pressure"
)

const (
	trashFilesDir = "files"
	trashInfoDir  = "info"
	trashInfoExt  = ".trashinfo"
	// deletionDateLayout is the local-time.
	deletionDateLayout = "2006-01-02T15:04:05"
)

type fdoTrash struct {
	home string // home trash directory ($XDG_DATA_HOME/Trash)
	uid  int
}

func fdoBackend() (bin.Backend, error) {
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

func (t *fdoTrash) Recycle(paths []string) ([]bin.Disposal, error) {
	var errs []error
	disposals := make([]bin.Disposal, 0, len(paths))
	for _, path := range paths {
		d, err := t.recycleOne(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("recycling %s: %w", path, err))
			continue
		}
		disposals = append(disposals, d)
	}
	return disposals, errors.Join(errs...)
}

func (t *fdoTrash) recycleOne(path string) (bin.Disposal, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return bin.Disposal{}, err
	}
	if _, err := os.Lstat(abs); err != nil {
		return bin.Disposal{}, err
	}
	if abs == filepath.Clean("/") {
		return bin.Disposal{}, errors.New("refusing to recycle the filesystem root")
	}

	dir, top, err := t.trashDirFor(abs)
	if err != nil {
		return bin.Disposal{}, err
	}

	// A bin is on the filesystem it takes from, so recycling frees nothing here.
	// Under the daemon's floor that stops being a deferral, and the deletion the
	// caller asked for is the only thing that gives space back.
	if decision := pressure.Check(abs, dir); decision.Permanent {
		if err := os.RemoveAll(abs); err != nil {
			return bin.Disposal{}, err
		}
		return bin.Disposal{Path: abs, Permanent: true, Reason: decision.Reason}, nil
	}

	// Measured before anything moves, so the record holds what went in.
	size := fsutil.TreeSize(abs)

	// A per-filesystem trash records the location relative to the top directory.
	recorded := abs
	if top != "" {
		if rel, err := filepath.Rel(top, abs); err == nil {
			recorded = rel
		}
	}

	name, info, err := createInfoFile(dir, filepath.Base(abs))
	if err != nil {
		return bin.Disposal{}, err
	}
	infoPath := filepath.Join(dir, trashInfoDir, name+trashInfoExt)
	_, err = fmt.Fprintf(info, "[Trash Info]\nPath=%s\nDeletionDate=%s\nSize=%d\n",
		escapePath(recorded), time.Now().Format(deletionDateLayout), size)
	if closeErr := info.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(infoPath)
		return bin.Disposal{}, err
	}

	if err := fsutil.Move(abs, filepath.Join(dir, trashFilesDir, name)); err != nil {
		os.Remove(infoPath)
		return bin.Disposal{}, err
	}
	return bin.Disposal{Path: abs}, nil
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
		recorded := set.New[string]()
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), trashInfoExt) {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), trashInfoExt)
			recorded.Add(name)
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
			if orig != "" && !filepath.IsAbs(orig) && top != "" {
				orig = filepath.Join(top, orig)
			}
			display := name
			if orig != "" {
				display = filepath.Base(orig)
			}
			deletedAt := info.deletedAt
			if deletedAt.IsZero() {
				deletedAt = st.ModTime()
			}
			size := info.size
			infoPath := filepath.Join(dir, trashInfoDir, entry.Name())
			if size == bin.SizeUnknown && sizeIsRecordable(infoPath) {
				// An entry another implementation wrote records no size.
				size = fsutil.TreeSize(file)
				if err := recordSize(infoPath, size); err != nil {
					// The number did not land, so nothing may be evicted on it.
					size = bin.SizeUnknown
				}
			}
			items = append(items, bin.Item{
				ID:           file,
				Name:         display,
				OriginalPath: orig,
				DeletedAt:    deletedAt,
				Size:         size,
				IsDir:        st.IsDir(),
			})
		}
		items = append(items, t.adoptUnrecorded(dir, recorded)...)
	}
	bin.Sort(items)
	return items, nil
}

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
		file := filepath.Join(dir, trashFilesDir, name)
		st, err := os.Lstat(file)
		if err != nil {
			continue
		}
		size := fsutil.TreeSize(file)
		f, err := os.OpenFile(filepath.Join(dir, trashInfoDir, name+trashInfoExt),
			os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		switch {
		case errors.Is(err, fs.ErrExist):
			// A recycle claimed the name after the read of info/.
			continue
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
		items = append(items, bin.Item{
			ID:        file,
			Name:      name,
			DeletedAt: st.ModTime(),
			Size:      size,
			IsDir:     st.IsDir(),
		})
	}
	return items
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

// evict destroys a recycled item and its metadata.
func (t *fdoTrash) Evict(id string) error {
	file, infoPath, err := t.resolveID(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(file); err != nil {
		return err
	}
	os.Remove(infoPath)
	return nil
}

// resolveID validates an item ID and returns the path.
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

// trashDirFor returns the trash directory a path should be recycled into.
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
	// Preferred location: an administrator-created $topdir/.Trash with the sticky bit.
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

// Dirs reports the trash directories the daemon watches the filesystems of.
func (t *fdoTrash) Dirs() []string { return t.trashDirs() }

// trashDirs returns every trash directory belonging to this user.
func (t *fdoTrash) trashDirs() []string {
	dirs := []string{t.home}
	seen := set.Of[string](t.home)
	for _, top := range Mounts() {
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

// topDirOfTrash returns the filesystem top directory a per-filesystem trash directory belongs to, or
// "".
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

// createInfoFile atomically claims a free name.
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

// recordSize appends a Size line to a .trashinfo file that has none, so the tree behind it is not
// walked again. A failure is returned rather than swallowed: a swallowed failure repeats the walk.
func recordSize(infoPath string, size int64) error {
	if size == bin.SizeUnknown {
		return nil
	}
	f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "Size=%d\n", size); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// sizeIsRecordable reports whether a Size line can be appended. A measurement
// that cannot be kept is a tree walk the next poll repeats.
func sizeIsRecordable(infoPath string) bool {
	f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false
	}
	return f.Close() == nil
}

func parseDeletionDate(value string) time.Time {
	for _, layout := range []string{deletionDateLayout, time.RFC3339} {
		if ts, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return ts
		}
	}
	return time.Time{}
}

// escapePath percent-encodes a path for a .trashinfo Path field, separators intact.
func escapePath(path string) string {
	u := url.URL{Path: path}
	return u.EscapedPath()
}
