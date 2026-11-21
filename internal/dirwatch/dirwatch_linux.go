//go:build linux

// dirwatch_linux.go holds mask.

package dirwatch

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// mask is every change that adds, removes, renames or finishes a write to an
// entry. A write is seen when its writer closes the file, so a busy writer
// does not send one event per write.
const mask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_CLOSE_WRITE | unix.IN_ATTRIB | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF |
	unix.IN_ONLYDIR | unix.IN_DONT_FOLLOW

// maxPending is how many records the watcher holds unread before it drops them and reports an overflow.
const maxPending = 1 << 16

// A record is one event as the kernel wrote it.
type record struct {
	wd   int32
	mask uint32
	name string
}

// A Watcher holds one inotify instance.
type Watcher struct {
	fd   int
	file *os.File
	dirs map[int32]string // watch descriptor -> directory
	wds  map[string]int32

	mu       sync.Mutex
	pending  []record
	overflow bool
	err      error
}

// New opens an inotify instance.
func New() (*Watcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) {
			return nil, ErrUnsupported
		}
		return nil, err
	}
	w := &Watcher{fd: fd, dirs: map[int32]string{}, wds: map[string]int32{}}
	// A non-blocking descriptor goes to the runtime poller, so the drain sleeps until there is something to read.
	w.file = os.NewFile(uintptr(fd), "inotify")
	go w.drain()
	return w, nil
}

// drain reads records until the watcher closes.
func (w *Watcher) drain() {
	buf := make([]byte, 64<<10)
	for {
		n, err := w.file.Read(buf)
		if err != nil {
			if !errors.Is(err, os.ErrClosed) {
				w.mu.Lock()
				w.err = err
				w.mu.Unlock()
			}
			return
		}
		w.mu.Lock()
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			nameAt := off + unix.SizeofInotifyEvent
			off = nameAt + int(ev.Len)
			if ev.Mask&unix.IN_Q_OVERFLOW != 0 || len(w.pending) >= maxPending {
				w.overflow = true
				continue
			}
			name := ""
			if ev.Len > 0 {
				raw := buf[nameAt:off]
				if i := bytes.IndexByte(raw, 0); i >= 0 {
					raw = raw[:i]
				}
				name = string(raw)
			}
			w.pending = append(w.pending, record{wd: ev.Wd, mask: ev.Mask, name: name})
		}
		w.mu.Unlock()
	}
}

// Add watches the entries of dir. A directory already watched costs one
// system call and changes nothing. ENOSPC means the user's watch limit
// (fs.inotify.max_user_watches) is spent.
func (w *Watcher) Add(dir string) error {
	dir = filepath.Clean(dir)
	wd, err := unix.InotifyAddWatch(w.fd, dir, mask)
	if err != nil {
		return err
	}
	w.dirs[int32(wd)] = dir
	w.wds[dir] = int32(wd)
	return nil
}

// Watching reports whether dir has a watch.
func (w *Watcher) Watching(dir string) bool {
	_, ok := w.wds[filepath.Clean(dir)]
	return ok
}

// Remove drops the watch on dir.
func (w *Watcher) Remove(dir string) {
	dir = filepath.Clean(dir)
	if wd, ok := w.wds[dir]; ok {
		unix.InotifyRmWatch(w.fd, uint32(wd))
		delete(w.wds, dir)
		delete(w.dirs, wd)
	}
}

// Len is the number of directories watched.
func (w *Watcher) Len() int { return len(w.wds) }

// Read answers every event drained since the last call.
func (w *Watcher) Read() (Batch, error) {
	w.mu.Lock()
	records, overflow, err := w.pending, w.overflow, w.err
	w.pending, w.overflow = nil, false
	w.mu.Unlock()
	if err != nil {
		return Batch{}, err
	}
	b := Batch{Overflow: overflow}
	for _, r := range records {
		dir, ok := w.dirs[r.wd]
		if !ok {
			continue
		}
		if r.mask&unix.IN_IGNORED != 0 {
			// The kernel dropped the watch: the directory is gone.
			delete(w.dirs, r.wd)
			if w.wds[dir] == r.wd {
				delete(w.wds, dir)
			}
			b.Events = append(b.Events, Event{Dir: dir})
			continue
		}
		b.Events = append(b.Events, Event{Dir: dir, Name: r.name})
	}
	return b, nil
}

// Close releases the instance and every watch on it, and stops the drain.
func (w *Watcher) Close() error {
	return w.file.Close()
}
