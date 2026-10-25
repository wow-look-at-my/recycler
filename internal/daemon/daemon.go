package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/cargotarget"
	"github.com/wow-look-at-my/recycler/internal/diskfree"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

const (
	// DefaultPollInterval is how often. A tick costs a statfs per filesystem.
	DefaultPollInterval = time.Second

	// freeTargetFraction and freeTargetCeiling.
	freeTargetFraction = 10
	freeTargetCeiling  = 1 << 30

	// recoverMultiple is how far past the trigger a sweep frees, for runway.
	recoverMultiple = 8

	// Looser than freeTargetFraction: the same fraction leaves no runway.
	recoverCeilingFraction = 5
)

// FreeTarget is the available bytes the daemon keeps on a filesystem of the given size.
func FreeTarget(total uint64) uint64 {
	if target := total / freeTargetFraction; target < freeTargetCeiling {
		return target
	}
	return freeTargetCeiling
}

// RecoverTarget is what a sweep frees up to after FreeTarget is crossed, bounded
// so a small filesystem is not emptied.
func RecoverTarget(total uint64) uint64 {
	recover := FreeTarget(total) * recoverMultiple
	if ceiling := total / recoverCeilingFraction; recover > ceiling {
		return ceiling
	}
	return recover
}

// An Eviction records an item the daemon destroyed to reclaim space.
type Eviction struct {
	Item  bin.Item
	Error error // non-nil when the item could not be removed
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
	items, err := b.List()
	if err != nil {
		return nil, nil, err
	}
	budgeted, items := settleBuildOutput(b, items, cargotarget.CapFromEnv(cargotarget.DefaultCap))
	// Resolving the probe here keeps sweepItems off the filesystem it decides about.
	evicted, pressures, err := sweepItems(b, items, b.Dirs(), func(dir string) (uint64, uint64, error) {
		return diskfree.Free(probePath(dir))
	})
	return append(budgeted, evicted...), pressures, err
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
	for _, it := range over {
		ev := Eviction{Item: it}
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
	free func(string) (uint64, uint64, error),
) ([]Eviction, []Pressure, error) {
	var evicted []Eviction
	var pressures []Pressure
	for _, group := range groupByFilesystem(items, dirs) {
		avail, total, err := free(group.probe)
		if err != nil {
			// An unreadable filesystem must not stop.
			continue
		}
		trigger := FreeTarget(total)
		if avail >= trigger {
			continue
		}
		// Crossing the target starts the sweep. Reaching it does not stop it.
		target := RecoverTarget(total)

		// The oldest goes at the front: the longer something.
		sort.Slice(group.items, func(i, j int) bool {
			return group.items[i].DeletedAt.Before(group.items[j].DeletedAt)
		})
		var stranded uint64
		for _, it := range group.items {
			if avail >= target {
				break
			}
			if it.Size == bin.SizeUnknown || it.Size < 0 {
				continue
			}
			ev := Eviction{Item: it}
			if err := b.Evict(it.ID); err != nil {
				ev.Error = fmt.Errorf("evicting %s: %w", it.ID, err)
				stranded += uint64(it.Size)
			} else {
				avail += uint64(it.Size)
			}
			evicted = append(evicted, ev)
		}
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
	return evicted, pressures, nil
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
	for {
		evicted, pressures, err := Sweep()
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
