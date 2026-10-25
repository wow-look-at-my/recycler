//go:build unix

package putback

<<<<<<< HEAD
// The "Put Back" records macOS keeps in a trash directory's .DS_Store: ptbL and
// ptbN. Depth: docs/ds-store-format.md. Built on every Unix, so Linux tests it.
=======
// The "Put Back" records macOS keeps for everything in the Trash, and the
// reading and writing of the .DS_Store file that holds them.
//
// Each trashed item has a location record and a name record in its trash
// directory's .DS_Store, keyed
// by the name the item has inside the trash:
//
//	      something was already called that
//
// Nothing else records where a trashed file came from, so this is what Finder's
// Put Back command reads, what other trash tools read, and what this package
// it reads and writes. Keeping the records here rather than in a private
// index is what makes Finder and this package interoperate.
//
// This file is built on every Unix so that the .DS_Store handling is exercised
// by the test suite, which runs on Linux.
>>>>>>> 21718d9 (commit)

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/wow-look-at-my/go-containers/set"
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

// Of splits an absolute original path into the records kept for an item
// trashed onto the volume mounted at root.
func Of(root, original string) Origin {
	dir := filepath.Dir(original)
	if rel, err := filepath.Rel(root, dir); err == nil && !strings.HasPrefix(rel, "..") {
		dir = rel
	}
	return Origin{Dir: dir, Name: filepath.Base(original)}
}

// path rebuilds the absolute original path of an item trashed onto the volume mounted at root.
func (p Origin) Path(root string) string {
	if p.Dir == "" || p.Name == "" {
		return ""
	}
	if filepath.IsAbs(p.Dir) {
		return filepath.Join(p.Dir, p.Name)
	}
	return filepath.Join(root, p.Dir, p.Name)
}

// TrashName picks the name. An item takes inside a trash directory, passing
// over the names in taken that are promised to items not moved in yet.
func TrashName(want, dir string, taken set.Set[string]) string {
	name := fsutil.UniqueName(want, taken, dir)
	if name == dsstore.FileName {
		name = fsutil.UniqueName("recycled"+dsstore.FileName, taken, dir)
	}
	return name
}

// Read returns the put back records of a trash directory, keyed by the name of the item inside it.
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

// Rewrites counts the .DS_Store rewrites this process has made.
var Rewrites atomic.Int64

// Set records where an item in a trash directory came from, leaving every
// other record in the .DS_Store alone.
func Set(dir, name string, origin Origin) error {
	return SetAll(dir, map[string]Origin{name: origin})
}

// SetAll records the origins of many items, keyed by their names in the trash
// directory, in a single rewrite of its .DS_Store.
func SetAll(dir string, origins map[string]Origin) error {
	names := set.New[string](len(origins))
	for name := range origins {
		names.Add(name)
	}
	return updateDSStore(dir, func(records []dsstore.Record) []dsstore.Record {
		records = withoutPutBacks(records, func(r dsstore.Record) bool { return names.Contains(r.Name) })
		for name, origin := range origins {
			records = append(records,
				dsstore.Ustr(name, putBackDirCode, origin.Dir),
				dsstore.Ustr(name, putBackNameCode, origin.Name))
		}
		return records
	})
}

// Retract drops the records SetAll wrote for items that never reached the
// trash. A record another writer has since replaced is left alone.
func Retract(dir string, origins map[string]Origin) error {
	return Amend(dir, origins, nil)
}

// Amend rewrites a directory's .DS_Store once, dropping the records named in
// drop and writing those in add. A record another writer has since replaced
// is left alone. A batch uses it to settle what its moves changed after the
// rewrite they were planned from.
func Amend(dir string, drop, add map[string]Origin) error {
	return updateDSStore(dir, func(records []dsstore.Record) []dsstore.Record {
		current := putBacksFrom(records)
		records = withoutPutBacks(records, func(r dsstore.Record) bool {
			ours, ok := drop[r.Name]
			return ok && current[r.Name] == ours
		})
		names := set.New[string](len(add))
		for name := range add {
			names.Add(name)
		}
		records = withoutPutBacks(records, func(r dsstore.Record) bool { return names.Contains(r.Name) })
		for name, origin := range add {
			records = append(records,
				dsstore.Ustr(name, putBackDirCode, origin.Dir),
				dsstore.Ustr(name, putBackNameCode, origin.Name))
		}
		return records
	})
}

// Clear drops the records of items that have left the trash.
func Clear(dir string, names ...string) error {
	gone := set.Of(names...)
	return updateDSStore(dir, func(records []dsstore.Record) []dsstore.Record {
		return withoutPutBacks(records, func(r dsstore.Record) bool { return gone.Contains(r.Name) })
	})
}

// withoutPutBacks removes the put back records drop picks, keeping everything else in the .DS_Store.
func withoutPutBacks(records []dsstore.Record, drop func(dsstore.Record) bool) []dsstore.Record {
	kept := records[:0]
	for _, r := range records {
		if (r.Code == putBackDirCode || r.Code == putBackNameCode) && drop(r) {
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

// updateDSStore rewrites a directory's .DS_Store with the records fn returns, holding.
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
		// Everything else a .DS_Store holds is display settings - icon positions.
		records, _ = dsstore.Parse(data)
	}
	updated, err := dsstore.Build(fn(records))
	if err != nil {
		return err
	}
	Rewrites.Add(1)
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt(updated, 0); err != nil {
		return err
	}
	return f.Sync()
}
