package cargotarget

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
)

// Build output in the recycle bin answers to a budget of its own. A trim moves
// the output into a bin on the same filesystem, which frees nothing until the
// bin gives it up. The daemon's floor gives it up only when the disk is nearly
// full. It also goes oldest first, so the user's own recycled files would go
// before newer build output. The budget makes a trim return the space it took.
// The bin keeps the newest build output up to the cap and gives back the rest.

// Overflow picks the build output a recycle bin holds past budget in each bin
// directory. The newest is kept until the next item would pass the budget,
// and that item and everything older go. An item is build output when the
// location it was recycled from lies inside a cargo target directory that is
// still there. Nothing else in the bin is ever chosen, and neither is an item
// whose size is unknown, since nothing can account for it.
func Overflow(items []bin.Item, budget int64) []bin.Item {
	known := map[string]bool{}
	var order []string
	byDir := map[string][]bin.Item{}
	for _, it := range items {
		if it.Size < 0 || !insideTarget(it.OriginalPath, known) {
			continue
		}
		dir := filepath.Dir(it.ID)
		if _, seen := byDir[dir]; !seen {
			order = append(order, dir)
		}
		byDir[dir] = append(byDir[dir], it)
	}

	var over []bin.Item
	for _, dir := range order {
		group := byDir[dir]
		slices.SortFunc(group, func(a, b bin.Item) int {
			if c := b.DeletedAt.Compare(a.DeletedAt); c != 0 {
				return c
			}
			return strings.Compare(a.ID, b.ID)
		})
		var kept int64
		for i, it := range group {
			if kept+it.Size > budget {
				over = append(over, group[i:]...)
				break
			}
			kept += it.Size
		}
	}
	return over
}

// insideTarget reports whether path lies strictly inside a cargo target
// directory. known remembers each directory already checked.
func insideTarget(path string, known map[string]bool) bool {
	if path == "" {
		return false
	}
	for dir := filepath.Dir(filepath.Clean(path)); ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == targetName {
			is, checked := known[dir]
			if !checked {
				is = IsCargoTarget(dir)
				known[dir] = is
			}
			if is {
				return true
			}
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// A Return is an item of build output the bin gave back.
type Return struct {
	Item bin.Item

	// Bytes is what the item occupied on its filesystem when it went.
	Bytes int64
}

// Settle evicts the build output b holds past budget, oldest first, and
// returns what went. An item that will not go is reported and the rest still
// go.
func Settle(b bin.Backend, budget int64) ([]Return, error) {
	items, err := b.List()
	if err != nil {
		return nil, err
	}
	var returned []Return
	var errs []error
	for _, it := range Overflow(items, budget) {
		bytes := fsutil.DiskUsage(it.ID)
		if err := b.Evict(it.ID); err != nil {
			errs = append(errs, fmt.Errorf("recycler: giving back %s: %w", it.ID, err))
			continue
		}
		returned = append(returned, Return{Item: it, Bytes: max(bytes, 0)})
	}
	return returned, errors.Join(errs...)
}
