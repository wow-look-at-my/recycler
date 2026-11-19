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
}

func newFolderWatch() *folderWatch {
	return &folderWatch{last: map[string]time.Time{}, over: set.New[string]()}
}

// tick walks each capped folder that is due for a walk.
func (w *folderWatch) tick(s Settings, now time.Time) []Eviction {
	var evicted []Eviction
	for _, f := range s.Folders {
		if now.Sub(w.last[f.Dir]) < folderInterval {
			continue
		}
		w.last[f.Dir] = now
		gone, left, err := sweepFolder(f, now)
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

// sweepFolder holds f.Dir to its cap.
func sweepFolder(f Folder, now time.Time) ([]Eviction, uint64, error) {
	return sweepFolderTo(f, f.limit(), now)
}

// sweepFolderTo gives back. The least recently used files under f.Dir until
// the files there take at most limit, which a test can set under MinFolderMax.
// It answers what it gave back and what the files there take afterwards.
//
// Every regular file counts toward the cap. Only this user's files that were
// not used within FolderGrace go. The walk never follows a symbolic link,
// never enters another filesystem, and never enters a recycle bin.
func sweepFolderTo(f Folder, limit uint64, now time.Time) ([]Eviction, uint64, error) {
	b, err := trash.Backend()
	if err != nil && !f.Delete {
		return nil, 0, err
	}
	var bins []string
	if b != nil {
		bins = b.Dirs()
	}
	files, total, err := walkFolder(f.Dir, bins, now)
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
// file there takes.
func walkFolder(dir string, bins []string, now time.Time) ([]bin.Item, uint64, error) {
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
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		size := max(fsutil.Allocated(info), 0)
		total += uint64(size)
		if mine && now.Sub(used) >= FolderGrace {
			files = append(files, bin.Item{ID: path, Name: d.Name(), OriginalPath: path, Size: size, DeletedAt: used})
		}
		return nil
	})
	return files, total, err
}
