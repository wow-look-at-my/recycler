// folder.go holds Folder.

package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/dirwatch"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

const (
	// MinFolderMax is the smallest cap a folder takes.
	MinFolderMax = 1 << 30

	// FolderGrace is how recently a file can have been used and still be safe from a folder cap.
	FolderGrace = time.Hour

	// folderInterval is how often the daemon walks each capped folder.
	folderInterval = time.Minute
)

// A Folder is a directory the daemon keeps under a size, giving back the
// least recently used files first.
type Folder struct {
	Dir string `json:"dir"`
	Max string `json:"max"` // a size such as "10GB"

	// Delete removes files permanently. Without it they go to the recycle bin.
	Delete bool `json:"delete,omitempty"`
}

func (f Folder) String() string {
	how := "recycling"
	if f.Delete {
		how = "deleting permanently"
	}
	return fmt.Sprintf("%s under %s, %s", f.Dir, f.Max, how)
}

// ParseFolder reads DIR:SIZE[:delete|:recycle], as the daemon command line spells a folder.
func ParseFolder(s string) (Folder, error) {
	parts := strings.Split(s, ":")
	f := Folder{}
	switch last := parts[len(parts)-1]; {
	case len(parts) >= 3 && last == "delete":
		f.Delete = true
		parts = parts[:len(parts)-1]
	case len(parts) >= 3 && last == "recycle":
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 2 {
		return f, fmt.Errorf("recycler: %q is not DIR:SIZE[:delete|:recycle]", s)
	}
	f.Dir = strings.Join(parts[:len(parts)-1], ":")
	f.Max = parts[len(parts)-1]
	return f, f.Validate()
}

// Validate refuses a cap low enough to empty the folder, and a folder that
// holds the home directory or the whole filesystem.
func (f Folder) Validate() error {
	a, err := ParseAmount(f.Max)
	if err != nil {
		return err
	}
	if a.Percent > 0 {
		return errors.New("recycler: a folder cap takes a size such as 10GB, not a percentage")
	}
	if a.Bytes < MinFolderMax {
		return fmt.Errorf("recycler: a folder cap of %s is under %s, which would give back nearly everything in it",
			a, formatBytes(MinFolderMax))
	}
	dir := filepath.Clean(f.Dir)
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("recycler: folder %q is not an absolute path", f.Dir)
	}
	if filepath.Dir(dir) == dir {
		return fmt.Errorf("recycler: folder %q is the root of a filesystem", f.Dir)
	}
	if home, err := accountHome(); err == nil && within(home, dir) {
		return fmt.Errorf("recycler: folder %q holds the home directory %s", f.Dir, home)
	}
	return nil
}

// limit answers the cap in bytes. Validate has already accepted it.
func (f Folder) limit() uint64 {
	a, _ := ParseAmount(f.Max)
	return a.Bytes
}

// within reports whether path is dir or sits under it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// folderWatch is what the daemon keeps between walks of the capped folders.
type folderWatch struct {
	last map[string]time.Time
	over set.Set[string] // folders the last walk could not get under their cap

	// watch is nil when every due folder is walked.
	watch     *dirwatch.Watcher
	watchedBy map[string]string // watched directory -> the folder.
	walked    set.Set[string]
	dirty     set.Set[string]
	blind     set.Set[string] // folders with a directory the watch could not take
	walks     int
}

// newFolderWatch walks each capped folder every folderInterval. It keeps
// nothing between calls that needs closing.
func newFolderWatch() *folderWatch {
	return &folderWatch{last: map[string]time.Time{}, over: set.New[string]()}
}

// newWatchedFolders walks a capped folder only when a directory watch saw it
// change, or while it is over its cap. A system with no directory watch walks
// each folder every folderInterval, and says so once.
func newWatchedFolders() *folderWatch {
	w := newFolderWatch()
	watch, err := dirwatch.New()
	if err != nil {
		notef("cannot watch capped folders (%v); each is walked every %s", err, folderInterval)
		return w
	}
	w.watch = watch
	w.watchedBy = map[string]string{}
	w.walked, w.dirty, w.blind = set.New[string](), set.New[string](), set.New[string]()
	return w
}

// drain marks each folder a watch saw change since the last tick.
func (w *folderWatch) drain(folders []Folder) {
	if w.watch == nil {
		return
	}
	batch, err := w.watch.Read()
	if err != nil || batch.Overflow {
		for _, f := range folders {
			w.dirty.Add(f.Dir)
		}
		return
	}
	for _, ev := range batch.Events {
		folder, ok := w.watchedBy[ev.Dir]
		if !ok {
			continue
		}
		w.dirty.Add(folder)
		if ev.Name == "" {
			// The directory is gone, and the kernel dropped its watch.
			delete(w.watchedBy, ev.Dir)
		}
	}
}

// due reports whether f needs a walk now.
func (w *folderWatch) due(f Folder, now time.Time) bool {
	if now.Sub(w.last[f.Dir]) < folderInterval {
		return false
	}
	if w.watch == nil {
		return true
	}
	// A folder over its cap is walked again because its files age past FolderGrace.
	return !w.walked.Contains(f.Dir) || w.dirty.Contains(f.Dir) || w.over.Contains(f.Dir) || w.blind.Contains(f.Dir)
}

// onDir answers what a walk of f calls on each directory it enters: it watches
// the directory. It answers nil when there is no watch.
func (w *folderWatch) onDir(f Folder) func(string) {
	if w.watch == nil {
		return nil
	}
	return func(dir string) {
		if err := w.watch.Add(dir); err != nil {
			if !w.blind.Contains(f.Dir) {
				notef("cannot watch %s (%v); %s is walked every %s", dir, err, f.Dir, folderInterval)
			}
			w.blind.Add(f.Dir)
			return
		}
		w.watchedBy[filepath.Clean(dir)] = f.Dir
	}
}

// close releases the watch.
func (w *folderWatch) close() {
	if w.watch != nil {
		w.watch.Close()
	}
}

// forget drops the watches of every folder no longer capped.
func (w *folderWatch) forget(folders []Folder) {
	if w.watch == nil {
		return
	}
	capped := set.New[string]()
	for _, f := range folders {
		capped.Add(f.Dir)
	}
	for dir, folder := range w.watchedBy {
		if !capped.Contains(folder) {
			w.watch.Remove(dir)
			delete(w.watchedBy, dir)
		}
	}
}

// tick walks each capped folder that is due for a walk.
func (w *folderWatch) tick(s Settings, now time.Time) []Eviction {
	w.drain(s.Folders)
	w.forget(s.Folders)
	var evicted []Eviction
	for _, f := range s.Folders {
		if !w.due(f, now) {
			continue
		}
		w.last[f.Dir] = now
		w.walks++
		if w.watch != nil {
			// Cleared before the walk, so a change during it marks the folder again.
			w.dirty.Remove(f.Dir)
			w.walked.Add(f.Dir)
		}
		gone, left, err := sweepFolderTo(f, f.limit(), now, w.onDir(f))
		evicted = append(evicted, gone...)
		if err != nil {
			notef("%s: %v", f.Dir, err)
			continue
		}
		switch over := left > f.limit(); {
		case over && !w.over.Contains(f.Dir):
			w.over.Add(f.Dir)
			notef("cannot keep %s under %s: it holds %s, and the rest is other users' files or was used in the last %s",
				f.Dir, f.Max, formatBytes(left), FolderGrace)
		case !over && w.over.Contains(f.Dir):
			w.over.Remove(f.Dir)
			notef("%s is back under %s", f.Dir, f.Max)
		}
	}
	return evicted
}

// sweepFolderTo gives back. The least recently used files under f.Dir until
// the files there take at most limit, which a test can set under MinFolderMax.
// It answers what it gave back and what the files there take afterwards.
//
// Every regular file counts toward the cap. Only this user's files that were
// not used within FolderGrace go. The walk never follows a symbolic link,
// never enters another filesystem, and never enters a recycle bin.
func sweepFolderTo(f Folder, limit uint64, now time.Time, onDir func(string)) ([]Eviction, uint64, error) {
	b, err := trash.Backend()
	if err != nil && !f.Delete {
		return nil, 0, err
	}
	var bins []string
	if b != nil {
		bins = b.Dirs()
	}
	files, total, err := walkFolder(f.Dir, bins, now, onDir)
	if err != nil {
		return nil, total, err
	}
	if total <= limit {
		return nil, total, nil
	}
	tracef("%s: its files take %s, over the %s cap; giving back the least recently used", f.Dir, formatBytes(total), f.Max)
	reason := fmt.Sprintf("least recently used in %s, which held %s, over its %s cap", f.Dir, formatBytes(total), f.Max)
	give := func(it bin.Item) error { return os.Remove(it.ID) }
	if !f.Delete {
		reason = "recycled as the " + reason
		give = func(it bin.Item) error {
			_, err := b.Recycle([]string{it.ID})
			return err
		}
	}
	evicted, freed, _ := drain(files, total-limit, everything, give, reason)
	for i := range evicted {
		evicted[i].LastUsed = true
	}
	tracef("%s: its files take %s after the sweep", f.Dir, formatBytes(total-freed))
	return evicted, total - freed, nil
}

// walkFolder answers this user's files under dir not used within FolderGrace, as
// items whose DeletedAt is the last use. walkFolder also answers what every regular
// file there takes. onDir, when not nil, is called on each directory the walk
// enters.
func walkFolder(dir string, bins []string, now time.Time, onDir func(string)) ([]bin.Item, uint64, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, 0, err
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return nil, 0, err
	}
	if !rootInfo.IsDir() {
		return nil, 0, fmt.Errorf("%s is not a directory", dir)
	}
	rootDev, _, _ := statOf(rootInfo)
	skip := set.New[string]()
	for _, b := range bins {
		skip.Add(filepath.Clean(b))
	}

	var total uint64
	var files []bin.Item
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An entry this user cannot read is not this user's to give back.
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		dev, mine, used := statOf(info)
		if d.IsDir() {
			if path != root && (dev != rootDev || skip.Contains(path)) {
				return fs.SkipDir
			}
			if onDir != nil {
				onDir(path)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		size := max(fsutil.Allocated(info), 0)
		total += uint64(size)
		if mine && now.Sub(used) >= FolderGrace {
			// Name the file under the folder as the person spelled it.
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			named := filepath.Join(dir, rel)
			files = append(files, bin.Item{ID: named, Name: d.Name(), OriginalPath: named, Size: size, DeletedAt: used})
		}
		return nil
	})
	return files, total, err
}
