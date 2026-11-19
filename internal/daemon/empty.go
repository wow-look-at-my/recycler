// empty.go holds EmptyCriteria.

package daemon

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

const (
	// EmptyGrace is how long an item stays in the bin before anything a person asks for can empty it.
	EmptyGrace = 24 * time.Hour

	// MinEmptyAge is the smallest age OlderThan takes.
	MinEmptyAge = EmptyGrace

	// MinBinBudget is the smallest bin MaxSize takes.
	MinBinBudget = 1 << 30
)

// EmptyCriteria select what an empty gives back. An item goes when any
// selector picks it, and the oldest items go first. A zero field is unset.
type EmptyCriteria struct {
	// OlderThan picks every item recycled longer ago than this.
	OlderThan time.Duration

	// MinFree picks the oldest items on each filesystem until it has this much free.
	MinFree Amount

	// MaxSize picks the oldest items until the whole bin weighs at most this.
	MaxSize uint64
}

// Validate refuses criteria that select nothing, and values low or high
// enough to empty the bin.
func (c EmptyCriteria) Validate() error {
	if c.OlderThan == 0 && c.MinFree.IsZero() && c.MaxSize == 0 {
		return errors.New("recycler: say what to empty: an age, a free-space goal or a size for the bin")
	}
	if c.OlderThan != 0 && c.OlderThan < MinEmptyAge {
		return fmt.Errorf("recycler: an age under %s would empty what was just recycled", MinEmptyAge)
	}
	if c.MaxSize != 0 && c.MaxSize < MinBinBudget {
		return fmt.Errorf("recycler: a bin size under %s would empty nearly all of it", formatBytes(MinBinBudget))
	}
	if c.MinFree.Percent > 100/MaxFreeFraction {
		return fmt.Errorf("recycler: a free-space goal over %d%% of the filesystem would empty all of it",
			100/MaxFreeFraction)
	}
	return nil
}

// An EmptyReport is what an empty gave back, or would give back.
type EmptyReport struct {
	// Evicted is in the order the items went, the oldest first.
	Evicted []Eviction

	// Unmet names each goal the empty could not reach, and why.
	Unmet []string
}

// Empty gives back what c selects, or with dryRun only reports it. An AI
// agent session may not empty the bin.
func Empty(c EmptyCriteria, dryRun bool) (EmptyReport, error) {
	if err := refuseAgent("empty the recycle bin"); err != nil {
		return EmptyReport{}, err
	}
	if err := c.Validate(); err != nil {
		return EmptyReport{}, err
	}
	b, err := trash.Backend()
	if err != nil {
		return EmptyReport{}, err
	}
	return emptyBin(b, c, freeAt, time.Now(), dryRun)
}

// emptyBin is Empty's body with the clock and the free-space probe injected.
func emptyBin(b bin.Backend, c EmptyCriteria, free func(string) (uint64, uint64, error),
	now time.Time, dryRun bool,
) (EmptyReport, error) {
	items, err := b.List()
	if err != nil {
		return EmptyReport{}, err
	}
	groups := groupByFilesystem(items, b.Dirs())
	type fs struct{ avail, want uint64 }
	space := make([]fs, len(groups))
	if !c.MinFree.IsZero() {
		// Every filesystem is read before anything goes. A refused goal
		// then refuses the whole run.
		for i, group := range groups {
			avail, total, err := free(group.probe)
			if err != nil {
				return EmptyReport{}, fmt.Errorf("recycler: reading free space for %s: %w", group.dir, err)
			}
			want := c.MinFree.Of(total)
			if want > total/MaxFreeFraction {
				return EmptyReport{}, fmt.Errorf("recycler: %s free on %s is over half of its %s; that would empty all of it",
					formatBytes(want), group.dir, formatBytes(total))
			}
			space[i] = fs{avail, want}
		}
	}

	var report EmptyReport
	chosen := map[string]string{}
	eligible := func(it bin.Item) bool {
		_, taken := chosen[it.ID]
		return !taken && now.Sub(it.DeletedAt) >= EmptyGrace
	}
	known := func(it bin.Item) bool { return it.Size >= 0 }
	// choose is the give of a planned empty: nothing goes until the plan is whole.
	choose := func(reason string) func(bin.Item) error {
		return func(it bin.Item) error {
			chosen[it.ID] = reason
			return nil
		}
	}

	if c.OlderThan != 0 {
		reason := "recycled more than " + formatAge(c.OlderThan) + " ago"
		for _, group := range groups {
			for _, it := range group.items {
				if eligible(it) && now.Sub(it.DeletedAt) > c.OlderThan {
					chosen[it.ID] = reason
				}
			}
		}
	}

	if !c.MinFree.IsZero() {
		for i, group := range groups {
			avail := space[i].avail
			for _, it := range group.items {
				if _, taken := chosen[it.ID]; taken && known(it) {
					avail += uint64(it.Size)
				}
			}
			if avail < space[i].want {
				reason := fmt.Sprintf("to bring %s to %s free", group.dir, formatBytes(space[i].want))
				_, freed, _ := drain(group.items, space[i].want-avail, eligible, choose(reason), reason)
				avail += freed
			}
			if avail < space[i].want {
				report.Unmet = append(report.Unmet, fmt.Sprintf("%s stays at %s free, under the %s asked for: what is left is live files, items recycled in the last %s, or items of unknown size",
					group.dir, formatBytes(avail), formatBytes(space[i].want), formatAge(EmptyGrace)))
			}
		}
	}

	if c.MaxSize != 0 {
		var weight uint64
		var all []bin.Item
		for _, group := range groups {
			for _, it := range group.items {
				if _, taken := chosen[it.ID]; !taken && known(it) {
					weight += uint64(it.Size)
				}
				all = append(all, it)
			}
		}
		if weight > c.MaxSize {
			reason := "to bring the bin to " + formatBytes(c.MaxSize)
			_, freed, _ := drain(all, weight-c.MaxSize, eligible, choose(reason), reason)
			weight -= freed
		}
		if weight > c.MaxSize {
			report.Unmet = append(report.Unmet, fmt.Sprintf("the bin stays at %s, over the %s asked for: what is left was recycled in the last %s",
				formatBytes(weight), formatBytes(c.MaxSize), formatAge(EmptyGrace)))
		}
	}

	for _, it := range items {
		if reason, ok := chosen[it.ID]; ok {
			report.Evicted = append(report.Evicted, Eviction{Item: it, Reason: reason})
		}
	}
	sort.SliceStable(report.Evicted, func(a, b int) bool {
		return report.Evicted[a].Item.DeletedAt.Before(report.Evicted[b].Item.DeletedAt)
	})
	if dryRun {
		return report, nil
	}
	evict := evictFrom(b)
	for i := range report.Evicted {
		report.Evicted[i].Error = evict(report.Evicted[i].Item)
	}
	return report, nil
}

// formatAge spells a duration in days where it is a whole number of them.
func formatAge(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return d.String()
}
