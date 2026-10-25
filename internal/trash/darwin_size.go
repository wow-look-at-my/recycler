//go:build darwin || cosmo

// macOS records no size for a trashed item, and the daemon lists the bin on every tick.

package trash

import (
	"io/fs"
	"sync"
	"syscall"
	"time"

	"github.com/wow-look-at-my/recycler/internal/fsutil"
)

// macOS records no size for a trashed item, and the daemon lists the bin on
// every tick.

// sizeKey names one entry in a trash directory. A rename, a replacement or a
// change at the entry's top level gives it a different key.
type sizeKey struct {
	path     string
	dev, ino uint64
	ctime    time.Time
}

// sizes holds what the last listing measured. Each listing replaces it, so an
// item that left the bin leaves it too.
var sizes struct {
	sync.Mutex
	known map[sizeKey]int64
}

// A sizer measures the entries of one listing against what the last one knew.
type sizer struct {
	last, next map[sizeKey]int64
}

func newSizer() *sizer {
	sizes.Lock()
	last := sizes.known
	sizes.Unlock()
	return &sizer{last: last, next: map[sizeKey]int64{}}
}

// size answers the size of the entry at file, walking its tree only when no
// earlier listing measured this same entry.
func (s *sizer) size(file string, info fs.FileInfo) int64 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fsutil.TreeSize(file)
	}
	key := sizeKey{path: file, dev: uint64(st.Dev), ino: uint64(st.Ino), ctime: ctime(st)}
	size, seen := s.last[key]
	if !seen {
		size = fsutil.TreeSize(file)
	}
	s.next[key] = size
	return size
}

// keep makes this listing's sizes the ones the next listing reuses.
func (s *sizer) keep() {
	sizes.Lock()
	sizes.known = s.next
	sizes.Unlock()
}
