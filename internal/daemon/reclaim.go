package daemon

import (
	"fmt"
	"sort"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

// Reclaim gives back recycled items, oldest first, until it has freed atLeast bytes or the bin is
// out of items whose size is known. It reports the bytes it accounted for.
//
// The poll loop reads free space and acts on a threshold. This does not: the caller has already met
// a full filesystem, which is the pressure the threshold exists to predict. A write that failed
// cannot wait for the next tick.
func Reclaim(atLeast uint64) (freed uint64, evicted []Eviction, err error) {
	b, err := trash.Backend()
	if err != nil {
		return 0, nil, err
	}
	items, err := b.List()
	if err != nil {
		return 0, nil, err
	}
	return reclaimItems(b, items, atLeast)
}

// reclaimItems is Reclaim's body against an already-read listing.
func reclaimItems(b bin.Backend, items []bin.Item, atLeast uint64) (uint64, []Eviction, error) {
	// The oldest goes first: the longer something has sat in the bin, the less it is wanted.
	sort.Slice(items, func(i, j int) bool {
		return items[i].DeletedAt.Before(items[j].DeletedAt)
	})

	var freed uint64
	var evicted []Eviction
	for _, it := range items {
		if freed >= atLeast {
			break
		}
		// An item of unrecorded size cannot be accounted for, so evicting it proves nothing about
		// the space that came back.
		if it.Size == bin.SizeUnknown || it.Size < 0 {
			continue
		}
		ev := Eviction{Item: it}
		if err := b.Evict(it.ID); err != nil {
			ev.Error = fmt.Errorf("evicting %s: %w", it.ID, err)
		} else {
			freed += uint64(it.Size)
		}
		evicted = append(evicted, ev)
	}
	return freed, evicted, nil
}
