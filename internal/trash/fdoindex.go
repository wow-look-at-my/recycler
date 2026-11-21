//go:build linux || freebsd || netbsd || openbsd || dragonfly || solaris

// fdoIndex keeps a listing of every FreeDesktop trash directory current from directory watches.

package trash

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-containers/set"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/dirwatch"
)

// dirsInterval is how often the index reads the mount table again for a trash directory that appeared on another filesystem.
const dirsInterval = time.Minute

// fdoIndex keeps a listing of every trash directory current from watches on
// each directory, its files/ and its info/.
type fdoIndex struct {
	t     *fdoTrash
	w     *dirwatch.Watcher
	now   func() time.Time
	dirs  []string
	known set.Set[string]
	read  time.Time                      // when dirs was last read
	items map[string]map[string]bin.Item // trash directory -> entry name -> item
	list  []bin.Item
	reads int // full reads of a trash directory
}

// Watch answers an index of this user's bin, which inotify keeps current.
func (t *fdoTrash) Watch() (bin.Index, error) {
	w, err := dirwatch.New()
	if err != nil {
		return nil, err
	}
	x := &fdoIndex{t: t, w: w, now: time.Now, known: set.New[string](), items: map[string]map[string]bin.Item{}}
	if _, err := x.Refresh(); err != nil {
		w.Close()
		return nil, err
	}
	return x, nil
}

func (x *fdoIndex) List() []bin.Item { return x.list }
func (x *fdoIndex) Dirs() []string   { return x.dirs }
func (x *fdoIndex) Close() error     { return x.w.Close() }

func (x *fdoIndex) Refresh() ([]string, error) {
	full := set.New[string]()
	names := map[string]map[string]bool{}
	var changed []string
	if now := x.now(); x.read.IsZero() || now.Sub(x.read) >= dirsInterval {
		x.read = now
		dirs := x.t.trashDirs()
		next := set.Of(dirs...)
		for _, d := range dirs {
			if !x.known.Contains(d) {
				full.Add(d)
			}
		}
		for _, d := range x.dirs {
			if !next.Contains(d) {
				x.forget(d)
				changed = append(changed, d)
			}
		}
		x.dirs, x.known = dirs, next
	}
	for _, d := range x.dirs {
		// A directory missing at the last read has no watch to report its
		// creation. Trying again costs a few failed system calls.
		if !full.Contains(d) && !x.watched(d) {
			full.Add(d)
		}
	}

	batch, err := x.w.Read()
	if err != nil {
		return nil, err
	}
	if batch.Overflow {
		for _, d := range x.dirs {
			full.Add(d)
		}
	}
	for _, ev := range batch.Events {
		d, sub := x.locate(ev.Dir)
		switch {
		case d == "":
		case sub == "":
			// The trash directory itself. Only its files/ and info/ matter: the
			// directorysizes cache beside them changes on every recycle.
			if ev.Name == "" || ev.Name == trashFilesDir || ev.Name == trashInfoDir {
				full.Add(d)
			}
		case ev.Name == "":
			full.Add(d)
		case sub == trashInfoDir:
			if name, ok := strings.CutSuffix(ev.Name, trashInfoExt); ok {
				addName(names, d, name)
			}
		default:
			addName(names, d, ev.Name)
		}
	}

	for d := range full.All() {
		had := len(x.items[d])
		if err := x.readAll(d); err != nil {
			return nil, err
		}
		if had > 0 || len(x.items[d]) > 0 {
			changed = append(changed, d)
		}
	}
	for d, these := range names {
		if full.Contains(d) || !x.known.Contains(d) {
			continue
		}
		for name := range these {
			x.readName(d, name)
		}
		changed = append(changed, d)
	}
	if len(changed) > 0 {
		// A new slice, so a listing a caller still holds does not change under it.
		x.list = make([]bin.Item, 0, len(x.list))
		for _, d := range x.dirs {
			for _, it := range x.items[d] {
				x.list = append(x.list, it)
			}
		}
		bin.Sort(x.list)
	}
	return changed, nil
}

func addName(names map[string]map[string]bool, dir, name string) {
	if names[dir] == nil {
		names[dir] = map[string]bool{}
	}
	names[dir][name] = true
}

// locate answers the trash directory a watched directory belongs to, and
// which of its subdirectories that is: files. This also covers info, or
// "" for the trash directory itself.
func (x *fdoIndex) locate(watched string) (dir, sub string) {
	if x.known.Contains(watched) {
		return watched, ""
	}
	parent, base := filepath.Dir(watched), filepath.Base(watched)
	if x.known.Contains(parent) && (base == trashFilesDir || base == trashInfoDir) {
		return parent, base
	}
	return "", ""
}

// watched reports whether the trash directory d, its files/ and its info/ all
// have a watch.
func (x *fdoIndex) watched(d string) bool {
	return x.w.Watching(d) && x.w.Watching(filepath.Join(d, trashFilesDir)) && x.w.Watching(filepath.Join(d, trashInfoDir))
}

// readAll watches the trash directory d and lists it again in full.
func (x *fdoIndex) readAll(d string) error {
	for _, w := range []string{d, filepath.Join(d, trashFilesDir), filepath.Join(d, trashInfoDir)} {
		if err := x.w.Add(w); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	x.reads++
	items := map[string]bin.Item{}
	for _, it := range x.t.listDir(d) {
		items[filepath.Base(it.ID)] = it
	}
	x.items[d] = items
	return nil
}

// readName reads the entry name in the trash directory d again.
func (x *fdoIndex) readName(d, name string) {
	it, ok := readEntry(d, x.t.topOf(d), name)
	if !ok {
		if _, err := os.Lstat(filepath.Join(d, trashInfoDir, name+trashInfoExt)); errors.Is(err, fs.ErrNotExist) {
			it, ok = adoptOne(d, name)
		}
	}
	if x.items[d] == nil {
		x.items[d] = map[string]bin.Item{}
	}
	if ok {
		x.items[d][name] = it
	} else {
		delete(x.items[d], name)
	}
}

// forget drops a trash directory that is no longer one of this user's.
func (x *fdoIndex) forget(d string) {
	for _, w := range []string{d, filepath.Join(d, trashFilesDir), filepath.Join(d, trashInfoDir)} {
		x.w.Remove(w)
	}
	delete(x.items, d)
}
