// A watch is what the daemon keeps between ticks.

package daemon

import (
	"os"
	"path/filepath"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

const (
	// settleInterval is how long a changed bin waits for its next listing.
	settleInterval = time.Minute

	// relistInterval bounds how long an unchanged bin goes unlisted.
	relistInterval = 10 * time.Minute

	// racyWindow is how new a change time must be to hide a second change.
	racyWindow = 2 * time.Second
)

// A stamp holds the change times of the directories an item enters or leaves one group through.
type stamp [3]int64

// A watch is what the daemon keeps between ticks. Every tick reads free space
// on every filesystem holding a bin. Where the backend keeps an index, the
// directory watches feed it and a listing reads nothing. Elsewhere a listing
// costs several system calls for each recycled item. A tick lists only when
// that can change the sweep.
type watch struct {
	backend func() (bin.Backend, error)
	free    func(dir string) (avail, total uint64, err error)
	now     func() time.Time

	// settings is nil in a test that drives the built-in settings.
	settings *settingsCache
	s        Settings // what the last tick ran.

	listed   time.Time // when the bin was last listed
	groups   []string
	stamps   map[string]stamp // group -> the change times that listing covers
	short    set.Set[string]  // groups under their trigger when it started
	freed    set.Set[string]
	stranded map[string]uint64 // group -> bytes it could not evict

	// index is the listing directory watches keep current.
	index   bin.Index
	noIndex bool
	pending set.Set[string] // groups the index saw change since the last sweep
}

func newWatch() *watch {
	return &watch{
		backend: trash.Backend,
		free:    freeAt,
		now:     time.Now,
		short:   set.New[string](),
		freed:   set.New[string](),
		pending: set.New[string](),

		settings: &settingsCache{path: SettingsPath},
	}
}

// close releases the directory watches.
func (w *watch) close() {
	if w.index != nil {
		w.index.Close()
	}
}

// indexed lists the bin from an index, and reads nothing to do it.
type indexed struct {
	bin.Backend
	index bin.Index
}

func (x indexed) List() ([]bin.Item, error) { return x.index.List(), nil }
func (x indexed) Dirs() []string            { return x.index.Dirs() }

// watchBin answers b listed from directory watches. It answers b itself where
// the backend keeps no index, or the watches failed.
func (w *watch) watchBin(b bin.Backend) bin.Backend {
	if w.index == nil && !w.noIndex {
		wb, ok := b.(bin.Watchable)
		if !ok {
			w.noIndex = true
			return b
		}
		index, err := wb.Watch()
		if err != nil {
			w.noIndex = true
			notef("cannot watch the recycle bin (%v); it reads the bin again each time its directories change", err)
			return b
		}
		w.index = index
	}
	if w.index == nil {
		return b
	}
	changed, err := w.index.Refresh()
	if err != nil {
		notef("lost the watch on the recycle bin (%v); it reads the bin again each time its directories change", err)
		w.index.Close()
		w.index, w.noIndex = nil, true
		// What the index held can no longer be trusted, so the next tick lists.
		w.listed = time.Time{}
		return b
	}
	for _, dir := range changed {
		w.pending.Add(filepath.Join(dir, "files"))
	}
	return indexed{Backend: b, index: w.index}
}

// tick is one pass of the daemon loop. It reports the same things [Sweep]
// does.
func (w *watch) tick() ([]Eviction, []Pressure, error) {
	b, err := w.backend()
	if err != nil {
		return nil, nil, err
	}
	b = w.watchBin(b)
	watched := w.index != nil
	dirs := b.Dirs()
	s := Settings{}
	if w.settings != nil {
		s = w.settings.load()
	}
	w.s = s
	now := w.now()
	age := now.Sub(w.listed)
	// The watches see every change, so an unchanged bin is never listed again.
	list := w.listed.IsZero() || (!watched && age >= relistInterval)

	// The stamps are read before the listing. A change during the listing then shows on the next tick.
	stamps := map[string]stamp{}
	short := set.New[string]()
	var pressures []Pressure
	for _, group := range w.groupsNow(dirs) {
		var changed bool
		if watched {
			changed = w.pending.Contains(group)
		} else {
			stamps[group] = stampOf(group)
			changed = stamps[group] != w.stamps[group]
		}
		under := false
		if avail, total, err := w.free(group); err == nil && avail < s.Trigger(total) {
			under = true
			short.Add(group)
			pressures = append(pressures, Pressure{
				Dir:      group,
				Avail:    avail,
				Target:   s.Trigger(total),
				Total:    total,
				Stranded: w.stranded[group],
			})
		}
		switch {
		case under && (changed || !w.short.Contains(group) || w.freed.Contains(group)):
			// The last listing did not see this pressure, or this bin.
			list = true
		case age >= settleInterval && (changed || under):
			list = true
		}
	}
	if !list {
		return nil, pressures, nil
	}

	evicted, pressures, looked, err := sweepBin(b, dirs, w.free, s)
	if err != nil {
		// Nothing is recorded, so the next tick lists again.
		return nil, nil, err
	}
	w.listed, w.groups, w.short = now, looked, short
	w.pending = set.New[string]()
	w.stamps = map[string]stamp{}
	for group, s := range stamps {
		// A second change inside the clock's grain leaves a new stamp as it
		// is. Such a stamp is not kept, so the group still reads as changed.
		if newest := max(s[0], s[1], s[2]); newest < now.Add(-racyWindow).UnixNano() {
			w.stamps[group] = s
		}
	}
	w.freed = set.New[string]()
	for _, ev := range evicted {
		if ev.Error == nil {
			w.freed.Add(filepath.Dir(ev.Item.ID))
		}
	}
	w.stranded = map[string]uint64{}
	for _, p := range pressures {
		w.stranded[p.Dir] = p.Stranded
	}
	return evicted, pressures, nil
}

// groupsNow answers every group to read: one for each bin directory, and each
// group the last listing found through an item.
func (w *watch) groupsNow(dirs []string) []string {
	seen := set.New[string]()
	var groups []string
	add := func(group string) {
		if seen.Add(group) {
			groups = append(groups, group)
		}
	}
	for _, group := range groupByFilesystem(nil, dirs) {
		add(group.dir)
	}
	for _, group := range w.groups {
		add(group)
	}
	return groups
}

// stampOf reads the change times that move when an item enters or leaves
// group. A FreeDesktop group is the files directory of a bin, and the info
// directory beside it takes the record.
func stampOf(group string) stamp {
	paths := []string{group}
	if filepath.Base(group) == "files" {
		binDir := filepath.Dir(group)
		paths = append(paths, binDir, filepath.Join(binDir, "info"))
	}
	var s stamp
	for i, path := range paths {
		if st, err := os.Stat(path); err == nil {
			s[i] = st.ModTime().UnixNano()
		}
	}
	return s
}
