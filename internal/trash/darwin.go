//go:build darwin || cosmo

package trash

// macOS Trash implementation.

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

	"github.com/wow-look-at-my/go-containers/set"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/dsstore"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
	"github.com/wow-look-at-my/recycler/internal/pressure"
	"github.com/wow-look-at-my/recycler/internal/putback"
)

type macTrash struct {
	home string // ~/.Trash
	uid  int

	// mounts replaces the kernel's mount table when set.
	mounts func() []string
}

func macBackend() (bin.Backend, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("recycler: locating home directory: %w", err)
	}
	return &macTrash{home: filepath.Join(home, ".Trash"), uid: os.Getuid()}, nil
}

// A Recycle call writes each trash directory's put back records once for the
// whole call, before anything moves.

// A move is a path headed for a trash directory under a name promised to it.
type move struct {
	index     int // position in the paths handed to Recycle
	src, name string
	origin    putback.Origin
}

// A batch is every move into one trash directory.
type batch struct {
	dir   string
	moves []move
	taken set.Set[string]
}

func (t *macTrash) Recycle(paths []string) ([]bin.Disposal, error) {
	results := make([]*bin.Disposal, len(paths))
	failures := make([]error, len(paths))
	fail := func(i int, err error) { failures[i] = fmt.Errorf("recycling %s: %w", paths[i], err) }

	var batches []*batch
	byDir := map[string]*batch{}
	for i, path := range paths {
		abs, dir, deleted, err := t.prepare(path)
		if err != nil {
			fail(i, err)
			continue
		}
		if deleted != nil {
			results[i] = deleted
			continue
		}
		b := byDir[dir]
		if b == nil {
			b = &batch{dir: dir, taken: set.New[string]()}
			byDir[dir] = b
			batches = append(batches, b)
		}
		name := putback.TrashName(filepath.Base(abs), dir, b.taken)
		b.taken.Add(name)
		b.moves = append(b.moves, move{index: i, src: abs, name: name, origin: putback.Of(t.volumeRoot(dir), abs)})
	}

	for _, b := range batches {
		t.moveBatch(b, results, fail)
	}

	disposals := make([]bin.Disposal, 0, len(paths))
	for _, d := range results {
		if d != nil {
			disposals = append(disposals, *d)
		}
	}
	return disposals, errors.Join(failures...)
}

// prepare validates a path and answers the trash directory it goes to, or the
// disposal of a path pressure deleted outright.
func (t *macTrash) prepare(path string) (abs, dir string, deleted *bin.Disposal, err error) {
	if abs, err = filepath.Abs(path); err != nil {
		return "", "", nil, err
	}
	if _, err := os.Lstat(abs); err != nil {
		return "", "", nil, err
	}
	if abs == filepath.Clean("/") {
		return "", "", nil, errors.New("refusing to recycle the filesystem root")
	}

	dir, err = t.trashDirFor(abs)
	if err != nil {
		return "", "", nil, err
	}

	// A trash is on the volume it takes from, so recycling frees nothing there.
	// Under the daemon's floor the deletion the caller asked for is the only
	// thing that gives space back.
	if decision := pressure.Check(abs, dir); decision.Permanent {
		if err := os.RemoveAll(abs); err != nil {
			return "", "", nil, err
		}
		return abs, "", &bin.Disposal{Path: abs, Permanent: true, Reason: decision.Reason}, nil
	}
	return abs, dir, nil, err
}

// moveBatch records every origin in the batch in one rewrite, then hands each
// item to the trash. The system picks the name inside the trash, so an item can
// land under one this batch did not promise it. Only the records that need it -
// those of a move that failed, and those of an item the system named itself -
// cost a second rewrite. That rewrite covers the whole batch.
func (t *macTrash) moveBatch(b *batch, results []*bin.Disposal, fail func(int, error)) {
	origins := make(map[string]putback.Origin, len(b.moves))
	for _, m := range b.moves {
		origins[m.name] = m.origin
	}
	recordErr := putback.SetAll(b.dir, origins)

	drops := map[string]map[string]putback.Origin{}
	adds := map[string]map[string]putback.Origin{}
	moveErrs := map[int]error{}
	for _, m := range b.moves {
		dest, err := trashItem(m.src, b.dir)
		if err != nil {
			moveErrs[m.index] = err
			fail(m.index, err)
			recordOrigin(drops, b.dir, m.name, m.origin)
			continue
		}
		if recordErr != nil {
			fail(m.index, fmt.Errorf("moved to the trash, but its original location could not be recorded: %w", recordErr))
			continue
		}
		results[m.index] = &bin.Disposal{Path: m.src}
		dir, name := filepath.Dir(dest), filepath.Base(dest)
		if dir == b.dir && name == m.name {
			continue
		}
		recordOrigin(drops, b.dir, m.name, m.origin)
		recordOrigin(adds, dir, name, putback.Of(t.volumeRoot(dir), m.src))
	}
	if recordErr != nil {
		return
	}

	dirs := set.New[string](2)
	settled := make([]string, 0, 2)
	for _, pending := range []map[string]map[string]putback.Origin{drops, adds} {
		for dir := range pending {
			if dirs.Contains(dir) {
				continue
			}
			dirs.Add(dir)
			settled = append(settled, dir)
		}
	}
	if len(settled) == 0 {
		return
	}

	var settleErr error
	for _, dir := range settled {
		if err := putback.Amend(dir, drops[dir], adds[dir]); err != nil && settleErr == nil {
			settleErr = err
		}
	}
	if settleErr == nil {
		return
	}
	for index, err := range moveErrs {
		fail(index, errors.Join(err, fmt.Errorf("its put back record could not be withdrawn: %w", settleErr)))
	}
}

// recordOrigin notes the record a rewrite drops, or the one it writes, under
// the directory that rewrite belongs to.
func recordOrigin(records map[string]map[string]putback.Origin, dir, name string, origin putback.Origin) {
	if records[dir] == nil {
		records[dir] = map[string]putback.Origin{}
	}
	records[dir][name] = origin
}

func (t *macTrash) List() ([]bin.Item, error) {
	var items []bin.Item
	measure := newSizer()
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
				Size:      measure.size(file, info),
				IsDir:     info.IsDir(),
			}
			if origin, ok := origins[entry.Name()]; ok {
				item.OriginalPath = dataVolumePath(origin.Path(root))
				item.Name = origin.Name
			}
			items = append(items, item)
		}
	}
	measure.keep()
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

// Evict destroys a trashed item and the put back record naming it. The
// trash's .DS_Store does not keep growing with records for items long gone.
func (t *macTrash) Evict(id string) error {
	file, err := t.resolveID(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(file); err != nil {
		return err
	}
	return putback.Clear(filepath.Dir(file), filepath.Base(file))
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

// Dirs reports the trash directories the daemon watches the filesystems of.
func (t *macTrash) Dirs() []string { return t.trashDirs() }

// trashDirs returns every trash directory belonging to this user.
func (t *macTrash) trashDirs() []string {
	dirs := []string{t.home}
	for _, dir := range volumeTrashes(t.mountPoints(), t.uid) {
		if dir != t.home {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// mountPoints lists the mount points to look for a volume trash under.
func (t *macTrash) mountPoints() []string {
	if t.mounts != nil {
		return t.mounts()
	}
	return Mounts()
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

// deletedAt reports when an item reached the trash. macOS records no deletion
// time, so this takes the inode change time the rename updated.
func deletedAt(info fs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return ctime(st)
	}
	return info.ModTime()
}
