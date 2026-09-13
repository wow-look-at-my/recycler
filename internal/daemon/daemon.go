package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/wow-look-at-my/recycler/internal/bin"
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

	// recoverCeilingFraction bounds a sweep so a small filesystem is not
	// emptied. It has to be looser than freeTargetFraction: clamping to the
	// same fraction makes the recovery target equal the trigger on every
	// filesystem small enough to keep the fraction, which is no runway at all.
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
// could. Recycling defers a deletion, so the daemon gives back only what was
// recycled: a filesystem filling with files nobody recycled leaves it running,
// correct and powerless. Saying so is what the type is for, because a sweep that
// reports nothing is otherwise indistinguishable from a healthy one.
type Pressure struct {
	Dir    string // the recycle bin directory whose filesystem this is
	Avail  uint64 // available bytes after the sweep
	Target uint64 // available bytes the daemon wanted
	Total  uint64 // bytes on the filesystem this caller can reach

	// Stranded is what the sweep tried to give back and could not, because the
	// eviction itself failed.
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

// Sweep reclaims space on every filesystem holding a recycle bin, and reports
// every one still under its target once it has given back all it can.
func Sweep() ([]Eviction, []Pressure, error) {
	b, err := trash.Backend()
	if err != nil {
		return nil, nil, err
	}
	items, err := b.List()
	if err != nil {
		return nil, nil, err
	}
	return sweepItems(b, items, b.Dirs(), diskfree.Free)
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
		// The trigger is what this reports against, not the recovery target.
		// Stopping short of the runway is ordinary. Sitting under the trigger
		// with nothing left to give back is the state nobody hears about today.
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

// filesystemGroup is the set of items sharing.
type filesystemGroup struct {
	probe string
	items []bin.Item
}

// groupByFilesystem splits a listing by the trash directory each item.
func groupByFilesystem(items []bin.Item) []filesystemGroup {
	order := make([]string, 0, 4)
	byDir := make(map[string][]bin.Item, 4)
	for _, it := range items {
		dir := filepath.Dir(it.ID)
		if _, seen := byDir[dir]; !seen {
			order = append(order, dir)
		}
		byDir[dir] = append(byDir[dir], it)
	}
	groups := make([]filesystemGroup, 0, len(order))
	for _, dir := range order {
		groups = append(groups, filesystemGroup{probe: dir, items: byDir[dir]})
	}
	return groups
}

// Run sweeps every interval until ctx is done.
func Run(ctx context.Context, interval time.Duration, report func([]Eviction, error)) error {
	lock, err := LockPath()
	if err != nil {
		return err
	}
	unlock, free, err := tryLock(lock)
	if err != nil {
		return err
	}
	if !free {
		return bin.ErrDaemonRunning
	}
	defer unlock()

	raisePriority()

	if interval <= 0 {
		interval = DefaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		evicted, err := Sweep()
		if report != nil {
			report(evicted, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
