//go:build linux

// dirwatch_linux.go holds mask.

package dirwatch

import (
	"errors"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// mask is every change that adds, removes, renames or finishes a write to an
// entry. A write is seen when its writer closes the file, so a busy writer
// does not send one event per write.
const mask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_CLOSE_WRITE | unix.IN_ATTRIB | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF |
	unix.IN_ONLYDIR | unix.IN_DONT_FOLLOW

// A Watcher holds one inotify instance. Read never blocks.
type Watcher struct {
	fd   int
	dirs map[int32]string // watch descriptor -> directory
	wds  map[string]int32
	buf  []byte
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
	return &Watcher{fd: fd, dirs: map[int32]string{}, wds: map[string]int32{}, buf: make([]byte, 64<<10)}, nil
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

// Read drains every event the kernel holds.
func (w *Watcher) Read() (Batch, error) {
	var b Batch
	for {
		n, err := unix.Read(w.fd, w.buf)
		switch {
		case err == unix.EINTR:
			continue
		case err == unix.EAGAIN:
			return b, nil
		case err != nil:
			return b, err
		case n <= 0:
			return b, nil
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&w.buf[off]))
			nameAt := off + unix.SizeofInotifyEvent
			off = nameAt + int(ev.Len)
			if ev.Mask&unix.IN_Q_OVERFLOW != 0 {
				b.Overflow = true
				continue
			}
			dir, ok := w.dirs[ev.Wd]
			if !ok {
				continue
			}
			if ev.Mask&unix.IN_IGNORED != 0 {
				// The kernel dropped the watch: the directory is gone.
				delete(w.dirs, ev.Wd)
				if w.wds[dir] == ev.Wd {
					delete(w.wds, dir)
				}
				b.Events = append(b.Events, Event{Dir: dir})
				continue
			}
			name := ""
			if ev.Len > 0 {
				raw := w.buf[nameAt:off]
				for i, c := range raw {
					if c == 0 {
						raw = raw[:i]
						break
					}
				}
				name = string(raw)
			}
			b.Events = append(b.Events, Event{Dir: dir, Name: name})
		}
	}
}

// Close releases the instance and every watch on it.
func (w *Watcher) Close() error {
	return unix.Close(w.fd)
}
