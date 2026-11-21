// delete.go holds Delete.

package daemon

import (
	"fmt"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

// Delete permanently removes the items a person picked out of the bin by ID.
// It reports each item with what became of it.
func Delete(ids []string) ([]Eviction, error) {
	if err := refuseAgent("delete from the recycle bin"); err != nil {
		return nil, err
	}
	b, err := trash.Backend()
	if err != nil {
		return nil, err
	}
	return deleteFrom(b, ids)
}

func deleteFrom(b bin.Backend, ids []string) ([]Eviction, error) {
	items, err := b.List()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]bin.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	evict := evictFrom(b)
	evicted := make([]Eviction, 0, len(ids))
	for _, id := range ids {
		it, ok := byID[id]
		if !ok {
			evicted = append(evicted, Eviction{Item: bin.Item{ID: id}, Error: fmt.Errorf("%w: %s", bin.ErrNotFound, id)})
			continue
		}
		evicted = append(evicted, Eviction{Item: it, Reason: "deleted by hand", Error: evict(it)})
	}
	return evicted, nil
}
