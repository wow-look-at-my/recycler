// Package daemon keeps the recycle bin from filling the disk. Recycling defers
// a deletion rather than performing one, which only works while there is
// somewhere to defer it to: a bin nobody empties fills the filesystem, and the
// recycled copy is what is holding the space. This package keeps that promise
// affordable, by giving back the oldest items when the filesystem gets tight.
//
// This is the one thing in the module that destroys anything, and it is
// deliberately not reachable from the CLI's verbs: pressure decides, never a
// user typing a command.
package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/diskfree"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

const (
	// DefaultPollInterval is how often the daemon reads free space.
	DefaultPollInterval = 30 * time.Second

	// freeTargetFraction and freeTargetCeiling set how much room the daemon
	// keeps: a tenth of the filesystem, never asking for more than 1 GiB.
	// A tenth of a small disk is a realistic reserve, and on a large one a
	// tenth is far more than anything needs held open.
	freeTargetFraction = 10
	freeTargetCeiling  = 1 << 30
)

// FreeTarget returns the number of available bytes the daemon keeps on a
// filesystem of the given total size: min(10% of the filesystem, 1 GiB).
func FreeTarget(total uint64) uint64 {
	if target := total / freeTargetFraction; target < freeTargetCeiling {
		return target
	}
	return freeTargetCeiling
}

// An Eviction records one item the daemon destroyed to reclaim space.
type Eviction struct {
	Item  bin.Item
	Error error // non-nil when the item could not be removed
}

// Sweep reclaims space on every filesystem holding a recycle bin whose
// available space is under [FreeTarget], destroying the oldest items until it
// is met or the bin is empty. It returns what it evicted.
//
// Sizes come from what was recorded when each item was recycled. Nothing in
// the bin is walked or stat-ed to size it: the contents of a recycled item do
// not change, so the number taken at ingestion is still the number now, and a
// sweep that re-measured would walk the whole bin every poll.
//
// An item whose size was never recorded is left alone. It cannot be accounted
// for, and destroying something to reclaim an unknown quantity is not a trade
// this can make honestly.
func Sweep() ([]Eviction, error) {
	b, err := trash.Backend()
	if err != nil {
		return nil, err
	}
	items, err := b.List()
	if err != nil {
		return nil, err
	}
	return sweepItems(b, items, diskfree.Free)
}

// sweepItems is Sweep's body against an already-read listing and an injected
// free-space probe, which is what lets the tests put a filesystem under
// pressure without filling a real one.
func sweepItems(b bin.Backend, items []bin.Item, free func(string) (uint64, uint64, error)) ([]Eviction, error) {
	var evicted []Eviction
	for _, group := range groupByFilesystem(items) {
		avail, total, err := free(group.probe)
		if err != nil {
			// One unreadable filesystem must not stop the others, the same
			// way one unreadable trash directory does not stop a listing.
			continue
		}
		target := FreeTarget(total)
		if avail >= target {
			continue
		}

		// Oldest first: the longer something has sat in the bin unrestored,
		// the less likely anyone wants it back.
		sort.Slice(group.items, func(i, j int) bool {
			return group.items[i].DeletedAt.Before(group.items[j].DeletedAt)
		})
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
			} else {
				avail += uint64(it.Size)
			}
			evicted = append(evicted, ev)
		}
	}
	return evicted, nil
}

// filesystemGroup is the set of items sharing one filesystem, and a path on it
// to ask about free space.
type filesystemGroup struct {
	probe string
	items []bin.Item
}

// groupByFilesystem splits a listing by the trash directory each item sits in.
// A trash directory is per-filesystem by construction - that is what the
// FreeDesktop per-volume directories and the per-drive $Recycle.Bin are - so
// its own path is the right thing to ask for free space, and grouping by it
// keeps one full filesystem from evicting items that are not on it.
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

// Run sweeps every interval until ctx is done. A zero interval means
// [DefaultPollInterval]. Each sweep's evictions are handed to report, which may
// be nil.
//
// A sweep that fails is reported and the loop continues: a filesystem that
// cannot be read now is usually one that can be read on the next tick, and a
// daemon that exits on the first error stops guarding every other filesystem
// too.
//
// It returns [bin.ErrDaemonRunning] when another daemon already holds the lock,
// so a second one started by hand stands down rather than sweeping in parallel.
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
