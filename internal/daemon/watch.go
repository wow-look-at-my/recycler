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
// on every filesystem holding a bin. A listing costs several system calls for
// each recycled item, so a tick lists only when that can change the sweep.
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
}

func newWatch() *watch {
	return &watch{
		backend: trash.Backend,
		free:    freeAt,
		now:     time.Now,
		short:   set.New[string](),
		freed:   set.New[string](),

		settings: &settingsCache{path: SettingsPath},
	}
}

// tick is one pass of the daemon loop. It reports the same things [Sweep]
// does.
func (w *watch) tick() ([]Eviction, []Pressure, error) {
	b, err := w.backend()
	if err != nil {
		return nil, nil, err
	}
	dirs := b.Dirs()
	s := Settings{}
	if w.settings != nil {
		s = w.settings.load()
	}
	w.s = s
	now := w.now()
	age := now.Sub(w.listed)
	list := w.listed.IsZero() || age >= relistInterval

	// The stamps are read before the listing. A change during the listing then shows on the next tick.
	stamps := map[string]stamp{}
	short := set.New[string]()
	var pressures []Pressure
	for _, group := range w.groupsNow(dirs) {
		stamps[group] = stampOf(group)
		changed := stamps[group] != w.stamps[group]
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
