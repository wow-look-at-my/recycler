// DefaultPollInterval is how often.

package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/cargotarget"
	"github.com/wow-look-at-my/recycler/internal/diskfree"
	"github.com/wow-look-at-my/recycler/internal/pressure"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

const (
	// DefaultPollInterval is how often the daemon reads free space. A tick costs a statfs per filesystem.
	DefaultPollInterval = time.Second

	// recoverMultiple is how far past the trigger a sweep frees, for runway.
	recoverMultiple = 2

	// The bound on the runway. A sweep never frees past a fifth of the filesystem.
	recoverCeilingFraction = 5
)

// FreeTarget is the available bytes the daemon keeps on a filesystem of the given size.
func FreeTarget(total uint64) uint64 {
	return pressure.Floor(total)
}

// RecoverTarget is what a sweep frees up to after FreeTarget is crossed, bounded
// so a small filesystem is not emptied.
func RecoverTarget(total uint64) uint64 {
	return recoverFrom(FreeTarget(total), total)
}

// recoverFrom is the runway past trigger, never under the trigger itself.
func recoverFrom(trigger, total uint64) uint64 {
	recover := trigger * recoverMultiple
	if ceiling := total / recoverCeilingFraction; recover > ceiling {
		recover = ceiling
	}
	return max(recover, trigger)
}

// An Eviction records an item the daemon destroyed to reclaim space.
type Eviction struct {
	Item  bin.Item
	Error error // non-nil when the item could not be removed

	// Reason says which rule chose the item.
	Reason string

	// LastUsed marks a file from the temporary directory, which was never in the bin.
	LastUsed bool
}

// Pressure is a filesystem left under its target after a sweep did what it
// could.
type Pressure struct {
	Dir    string
	Avail  uint64 // available bytes after the sweep
	Target uint64 // available bytes the daemon wanted
	Total  uint64 // bytes on the filesystem this caller can reach

	// Stranded is what the sweep tried to give back, and could not.
	Stranded uint64
}

// Shortfall is how many bytes the sweep could not reclaim.
func (p Pressure) Shortfall() uint64 {
	if p.Avail >= p.Target {
		return 0
	}
	return p.Target - p.Avail
}

func (p Pressure) String() string {
	return fmt.Sprintf("%s: %d bytes available, %d short of the %d byte target on a %d byte filesystem",
		p.Dir, p.Avail, p.Shortfall(), p.Target, p.Total)
}

// Sweep reclaims space on every filesystem holding a recycle bin. Sweep reports
// every a single still under its target a single time it has given back all it can.
func Sweep() ([]Eviction, []Pressure, error) {
	b, err := trash.Backend()
	if err != nil {
		return nil, nil, err
	}
	s, err := LoadSettings()
	if err != nil {
		return nil, nil, err
	}
	s = s.withFlags()
	active.Store(&s)
	tracef("settings: %s", s.describe())
	evicted, pressures, _, err := sweepBin(b, b.Dirs(), freeAt, s)
	if err != nil {
		return evicted, pressures, err
	}
	return append(evicted, newFolderWatch().tick(s, time.Now())...), pressures, nil
}

// freeAt reads free space at the nearest existing ancestor of dir.
func freeAt(dir string) (uint64, uint64, error) {
	return diskfree.Free(probePath(dir))
}

// sweepBin lists b and sweeps it. It also returns the directory of every
// group the sweep looked at.
func sweepBin(b bin.Backend, dirs []string, free func(string) (uint64, uint64, error), s Settings,
) ([]Eviction, []Pressure, []string, error) {
	items, err := b.List()
	if err != nil {
		return nil, nil, nil, err
	}
	budgeted, items := settleBuildOutput(b, items, cargotarget.CapFromEnv(cargotarget.DefaultCap))
	groups := groupByFilesystem(items, dirs)
	evicted, pressures := sweepGroups(b, groups, free, s)
	looked := make([]string, 0, len(groups))
	for _, group := range groups {
		looked = append(looked, group.dir)
	}
	return append(budgeted, evicted...), pressures, looked, nil
}

// settleBuildOutput gives back the build output the bin holds past budget,
// the cap a trim holds a target directory to. It returns what it tried to
// evict, and the listing without what went.
func settleBuildOutput(b bin.Backend, items []bin.Item, budget int64) ([]Eviction, []bin.Item) {
	over := cargotarget.Overflow(items, budget)
	if len(over) == 0 {
		return nil, items
	}
	gone := set.New[string]()
	evicted := make([]Eviction, 0, len(over))
	reason := "build output past the " + formatBytes(uint64(budget)) + " the bin keeps of it"
	tracef("the bin holds build output past its %s budget; giving back %d items", formatBytes(uint64(budget)), len(over))
	for _, it := range over {
		ev := Eviction{Item: it, Reason: reason}
		if err := b.Evict(it.ID); err != nil {
			ev.Error = fmt.Errorf("evicting %s: %w", it.ID, err)
		} else {
			gone.Add(it.ID)
		}
		evicted = append(evicted, ev)
	}
	kept := make([]bin.Item, 0, len(items)-gone.Len())
	for _, it := range items {
		if !gone.Contains(it.ID) {
			kept = append(kept, it)
		}
	}
	return evicted, kept
}

// sweepItems is Sweep's body against an already-read listing and an injected free-space probe.
func sweepItems(b bin.Backend, items []bin.Item, dirs []string,
	free func(string) (uint64, uint64, error), s Settings,
) ([]Eviction, []Pressure, error) {
	evicted, pressures := sweepGroups(b, groupByFilesystem(items, dirs), free, s)
	return evicted, pressures, nil
}

// sweepGroups evicts from each group whose filesystem is under its trigger.
func sweepGroups(b bin.Backend, groups []filesystemGroup,
	free func(string) (uint64, uint64, error), s Settings,
) ([]Eviction, []Pressure) {
	var evicted []Eviction
	var pressures []Pressure
	for _, group := range groups {
		avail, total, err := free(group.probe)
		if err != nil {
			// An unreadable filesystem must not stop.
			continue
		}
		trigger := s.Trigger(total)
		if avail >= trigger {
			continue
		}
		// Crossing the target starts the sweep. Reaching it does not stop it.
		target := s.Recover(total)
		tracef("%s: %s free of %s, under the %s trigger; giving back the oldest items until %s is free",
			group.dir, formatBytes(avail), formatBytes(total), formatBytes(trigger), formatBytes(target))
		reason := fmt.Sprintf("disk pressure: %s had %s free, under its %s trigger",
			group.dir, formatBytes(avail), formatBytes(trigger))

		gone, freed, stranded := drain(group.items, target-avail, everything, evictFrom(b), reason)
		evicted = append(evicted, gone...)
		avail += freed
		tracef("%s: %s free after the sweep", group.dir, formatBytes(avail))
		// Reported against the trigger, not the recovery target: stopping short
		// of the runway is ordinary, sitting under the trigger is not.
		if avail < trigger {
			pressures = append(pressures, Pressure{
				Dir:      group.dir,
				Avail:    avail,
				Target:   trigger,
				Total:    total,
				Stranded: stranded,
			})
		}
	}
	return evicted, pressures
}

// drain gives back the oldest items take accepts until need bytes are freed.
// Every rule that deletes by size goes through it: disk pressure, the
// selectors of an empty, and the cap on the temporary directory.
//
// An item of unknown size is kept, because giving it back cannot be accounted
// for. An item give fails on counts as stranded, not freed, and the next
// item takes its place. It answers what it gave back, the bytes freed and the
// bytes stranded.
func drain(items []bin.Item, need uint64, take func(bin.Item) bool, give func(bin.Item) error, reason string,
) (evicted []Eviction, freed, stranded uint64) {
	oldest := slices.Clone(items)
	// The oldest goes first: the longer something has gone unwanted, the less it is missed.
	sort.SliceStable(oldest, func(i, j int) bool { return oldest[i].DeletedAt.Before(oldest[j].DeletedAt) })
	for _, it := range oldest {
		if freed >= need {
			break
		}
		if !take(it) {
			continue
		}
		if it.Size < 0 {
			tracef("kept %s: its size is unknown, so giving it back cannot be accounted for", describeItem(it))
			continue
		}
		ev := Eviction{Item: it, Reason: reason}
		if err := give(it); err != nil {
			ev.Error = err
			stranded += uint64(it.Size)
		} else {
			freed += uint64(it.Size)
		}
		evicted = append(evicted, ev)
	}
	return evicted, freed, stranded
}

// everything is the take of a rule that may give back any item.
func everything(bin.Item) bool { return true }

// evictFrom answers a give that evicts from b.
func evictFrom(b bin.Backend) func(bin.Item) error {
	return func(it bin.Item) error {
		if err := b.Evict(it.ID); err != nil {
			return fmt.Errorf("evicting %s: %w", it.ID, err)
		}
		return nil
	}
}

// describeItem names an item by where it came from, or by its name when that is unknown.
func describeItem(it bin.Item) string {
	if it.OriginalPath != "" {
		return it.OriginalPath
	}
	return it.Name
}

// filesystemGroup is the items sharing a bin directory, plus the probe path.
type filesystemGroup struct {
	dir   string
	probe string
	items []bin.Item
}

// groupByFilesystem splits a listing by the trash directory each item sits in,
// and adds a group for every bin directory the listing never mentions.
func groupByFilesystem(items []bin.Item, dirs []string) []filesystemGroup {
	order := make([]string, 0, len(dirs)+4)
	byDir := make(map[string][]bin.Item, len(dirs)+4)
	add := func(dir string) {
		if _, seen := byDir[dir]; !seen {
			order = append(order, dir)
			byDir[dir] = nil
		}
	}
	for _, it := range items {
		dir := filepath.Dir(it.ID)
		add(dir)
		byDir[dir] = append(byDir[dir], it)
	}
	for _, dir := range dirs {
		// An ID names the files/ subdirectory, so spell a bin the same way here.
		add(filepath.Join(dir, "files"))
	}
	groups := make([]filesystemGroup, 0, len(order))
	for _, dir := range order {
		groups = append(groups, filesystemGroup{dir: dir, probe: dir, items: byDir[dir]})
	}
	return groups
}

// probePath answers the nearest existing ancestor of dir, which is what
// statfs can read.
func probePath(dir string) string {
	for {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// Run sweeps every interval until ctx is done.
func Run(ctx context.Context, interval time.Duration, standby bool, report func([]Eviction, []Pressure, error)) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return run(ctx, interval, selfIdentity(exe), standby, report)
}

// run is Run's body for a stated build, which is what lets a test drive both
// sides of a handover in one process.
func run(ctx context.Context, interval time.Duration, me Identity, standby bool, report func([]Eviction, []Pressure, error)) error {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	lock, err := LockPath()
	if err != nil {
		return err
	}
	unlock, held, err := acquire(ctx, lock, me, standby, interval)
	if err != nil {
		return err
	}
	clearHandover()
	withdraw := announce(me)
	defer func() {
		withdraw()
		if unlock != nil {
			unlock()
		}
	}()

	schedule()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	w := newWatch()
	defer w.close()
	folders := newWatchedFolders()
	defer folders.close()
	for {
		evicted, pressures, err := w.tick()
		evicted = append(evicted, folders.tick(w.s, time.Now())...)
		if report != nil {
			report(evicted, pressures, err)
		}
		if challenger, asked := pendingHandover(); asked && Newer(challenger, me) {
			withdraw()
			if unlock = standDown(lock, unlock, me); unlock == nil {
				return nil
			}
		}
		if lockMoved(lock, held) {
			// The lock this daemon holds no longer keeps a second one out.
			withdraw()
			withdraw = func() {}
			unlock()
			if unlock, held, err = acquire(ctx, lock, me, standby, interval); err != nil {
				return err
			}
			clearHandover()
			withdraw = announce(me)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// acquire takes the daemon lock and returns it with the file it locked. In
// standby, a daemon no newer than the one holding the lock polls until it can
// take it, where it would otherwise return [bin.ErrDaemonRunning].
func acquire(ctx context.Context, lock string, me Identity, standby bool, poll time.Duration) (func(), os.FileInfo, error) {
	for {
		unlock, err := takeLock(lock, me)
		if err == nil {
			held, err := os.Stat(lock)
			if err != nil {
				unlock()
				return nil, nil, err
			}
			return unlock, held, nil
		}
		if !standby || !errors.Is(err, bin.ErrDaemonRunning) {
			return nil, nil, err
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// lockMoved reports whether the lock path names a different file from held,
// or none.
func lockMoved(lock string, held os.FileInfo) bool {
	now, err := os.Stat(lock)
	return err != nil || !os.SameFile(now, held)
}
