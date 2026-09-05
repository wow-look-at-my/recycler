//go:build unix

package putback

// The "Put Back" records macOS keeps for everything in the Trash, and the
// reading and writing of the .DS_Store file that holds them.
//
// Each trashed item has two records in its trash directory's .DS_Store, keyed
// by the name the item has inside the trash:
//
//	ptbL  the original parent directory, relative to the volume root
//	ptbN  the original name, which differs from the name in the trash when
//	      something was already called that
//
// Nothing else records where a trashed file came from, so this is what Finder's
// Put Back command reads, what other trash tools read, and what this package
// both reads and writes. Keeping the records here rather than in a private
// index is what makes the two interoperate.
//
// This file is built on every Unix so that the .DS_Store handling is exercised
// by the test suite, which runs on Linux.

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/wow-look-at-my/recycler/internal/dsstore"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
)

const (
	putBackDirCode  = "ptbL"
	putBackNameCode = "ptbN"
)

// A Origin is where a trashed item came from, in the form macOS records it.
type Origin struct {
	Dir  string // original parent directory, relative to the volume root
	Name string // original name
}

// Of splits an absolute original path into the pair recorded for an item
// trashed onto the volume mounted at root.
func Of(root, original string) Origin {
	dir := filepath.Dir(original)
	if rel, err := filepath.Rel(root, dir); err == nil && !strings.HasPrefix(rel, "..") {
		dir = rel
	}
	return Origin{Dir: dir, Name: filepath.Base(original)}
}

// path rebuilds the absolute original path of an item trashed onto the volume
// mounted at root. It is empty when the location was never recorded. A location
// that is already absolute is taken as it stands, which is what makes a record
// written by some other trash implementation usable.
func (p Origin) Path(root string) string {
	if p.Dir == "" || p.Name == "" {
		return ""
	}
	if filepath.IsAbs(p.Dir) {
		return filepath.Join(p.Dir, p.Name)
	}
	return filepath.Join(root, p.Dir, p.Name)
}

// TrashName picks the name an item takes inside a trash directory. The
// directory's own .DS_Store holds the put back records, so nothing recycled
// takes that name however free it looks - the record keeps the item's real name
// regardless of what it is called in the trash.
func TrashName(want, dir string) string {
	name := fsutil.UniqueName(want, dir)
	if name == dsstore.FileName {
		name = fsutil.UniqueName("recycled"+dsstore.FileName, dir)
	}
	return name
}

// Read returns the put back records of a trash directory, keyed by the
// name of the item inside it. A missing, unreadable or unintelligible .DS_Store
// yields no records rather than an error: an item whose origin is unknown is
// still an item, and must still be listed.
func Read(dir string) map[string]Origin {
	data, err := os.ReadFile(filepath.Join(dir, dsstore.FileName))
	if err != nil {
		return nil
	}
	records, err := dsstore.Parse(data)
	if err != nil {
		return nil
	}
	return putBacksFrom(records)
}

// putBacksFrom picks the put back pairs out of a .DS_Store's records.
func putBacksFrom(records []dsstore.Record) map[string]Origin {
	origins := map[string]Origin{}
	for _, r := range records {
		if r.Code != putBackDirCode && r.Code != putBackNameCode {
			continue
		}
		value, ok := r.Ustr()
		if !ok {
			continue
		}
		origin := origins[r.Name]
		if r.Code == putBackDirCode {
			origin.Dir = value
		} else {
			origin.Name = value
		}
		origins[r.Name] = origin
	}
	for name, origin := range origins {
		if origin.Dir == "" {
			// A location is the point; a name on its own restores nothing.
			delete(origins, name)
			continue
		}
		if origin.Name == "" {
			origin.Name = name
			origins[name] = origin
		}
	}
	return origins
}

// Set records where an item now in a trash directory came from, leaving
// every other record in the .DS_Store alone.
func Set(dir, name string, origin Origin) error {
	return updateDSStore(dir, func(records []dsstore.Record) []dsstore.Record {
		records = withoutPutBack(records, name)
		return append(records,
			dsstore.Ustr(name, putBackDirCode, origin.Dir),
			dsstore.Ustr(name, putBackNameCode, origin.Name))
	})
}

// Clear drops the records of items that have left the trash.
func Clear(dir string, names ...string) error {
	return updateDSStore(dir, func(records []dsstore.Record) []dsstore.Record {
		for _, name := range names {
			records = withoutPutBack(records, name)
		}
		return records
	})
}

// withoutPutBack removes one item's put back records, keeping everything else
// the .DS_Store has to say about the directory.
func withoutPutBack(records []dsstore.Record, name string) []dsstore.Record {
	kept := records[:0]
	for _, r := range records {
		if r.Name == name && (r.Code == putBackDirCode || r.Code == putBackNameCode) {
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

// updateDSStore rewrites a directory's .DS_Store with the records fn returns,
// holding an exclusive lock so that two of these cannot race. Nothing can stop
// Finder from writing the same file at the same moment; that race is Apple's
// own, and loses put back information for Finder too.
func updateDSStore(dir string, fn func([]dsstore.Record) []dsstore.Record) error {
	f, err := os.OpenFile(filepath.Join(dir, dsstore.FileName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN) //nolint:errcheck // the file is about to be closed

	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	var records []dsstore.Record
	if len(data) > 0 {
		// Everything else a .DS_Store holds is display settings - icon
		// positions, window size - so a damaged one costs the trash window its
		// appearance at worst. Losing the put back records instead is not a
		// trade worth making.
		records, _ = dsstore.Parse(data)
	}
	updated, err := dsstore.Build(fn(records))
	if err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt(updated, 0); err != nil {
		return err
	}
	return f.Sync()
}
