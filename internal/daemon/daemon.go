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
	// DefaultPollInterval is how often. A tick that finds room costs one statfs
	// per filesystem, benchmarked at 1.4 microseconds, so looking every second
	// spends a millionth of a core. What the old 30s bought was a writer losing
	// up to 30 seconds of output against a target of a gigabyte: at 20 MB/s
	// that is 600 MB of the buffer gone between two looks.
	DefaultPollInterval = time.Second

	// freeTargetFraction and freeTargetCeiling.
	freeTargetFraction = 10
	freeTargetCeiling  = 1 << 30
)

// FreeTarget is the available bytes the daemon keeps on a filesystem of the given size.
func FreeTarget(total uint64) uint64 {
	if target := total / freeTargetFraction; target < freeTargetCeiling {
		return target
	}
	return freeTargetCeiling
}

// An Eviction records an item the daemon destroyed to reclaim space.
type Eviction struct {
	Item  bin.Item
	Error error // non-nil when the item could not be removed
}

// Sweep reclaims space on every filesystem holding a recycle bin whose available space.
func Sweep() ([]Eviction, error) {
	evicted, _, err := sweep()
	return evicted, err
}

// sweep is Sweep, also returning the filesystems it found a bin on so the poll
// loop can re-probe them without listing again.
func sweep() ([]Eviction, []string, error) {
	b, err := trash.Backend()
	if err != nil {
		return nil, nil, err
	}
	items, err := b.List()
	if err != nil {
		return nil, nil, err
	}
	probes := make([]string, 0, 4)
	for _, group := range groupByFilesystem(items) {
		probes = append(probes, group.probe)
	}
	evicted, err := sweepItems(b, items, diskfree.Free)
	return evicted, probes, err
}

// underPressure reports whether any of these filesystems has dropped below its
// FreeTarget. One statfs each, which is what lets the loop look often: listing
// the bin costs far more and is only worth paying once something is wrong.
func underPressure(probes []string, free func(string) (uint64, uint64, error)) bool {
	for _, probe := range probes {
		avail, total, err := free(probe)
		if err != nil {
			// An unreadable filesystem is the listing's problem, not this check's.
			return true
		}
		if avail < FreeTarget(total) {
			return true
		}
	}
	return false
}

// sweepItems is Sweep's body against an already-read listing and an injected free-space probe.
func sweepItems(b bin.Backend, items []bin.Item, free func(string) (uint64, uint64, error)) ([]Eviction, error) {
	var evicted []Eviction
	for _, group := range groupByFilesystem(items) {
		avail, total, err := free(group.probe)
		if err != nil {
			// An unreadable filesystem must not stop.
			continue
		}
		target := FreeTarget(total)
		if avail >= target {
			continue
		}

		// The oldest goes at the front: the longer something.
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

	if interval <= 0 {
		interval = DefaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// The filesystems a bin was last seen on. Empty forces the full path, which
	// is what refills it, so the first tick and any tick after an error list.
	var probes []string
	for {
		if len(probes) == 0 || underPressure(probes, diskfree.Free) {
			evicted, found, err := sweep()
			if err != nil {
				probes = nil
			} else {
				probes = found
			}
			if report != nil {
				report(evicted, err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
