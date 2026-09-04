package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/recycler"
)

// resolve turns a user-supplied reference into a recycle bin item. A reference
// is an ID as printed by "recycler list", an original path, or a file name -
// a path and a name resolve only when they match a single item.
func resolve(items []recycler.Item, ref string) (recycler.Item, error) {
	for _, item := range items {
		if item.ID == ref {
			return item, nil
		}
	}

	var matches []recycler.Item
	for _, item := range items {
		if pathsEqual(item.OriginalPath, ref) || pathsEqual(item.Name, ref) ||
			pathsEqual(item.OriginalPath, absOrSelf(ref)) {
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return recycler.Item{}, fmt.Errorf("%w: %s", recycler.ErrNotFound, ref)
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%q matches %d items in the recycle bin; use one of these IDs:", ref, len(matches))
		for _, item := range matches {
			fmt.Fprintf(&b, "\n  %s  (deleted %s)", item.ID, item.DeletedAt.Local().Format("2006-01-02 15:04:05"))
		}
		return recycler.Item{}, fmt.Errorf("%s", b.String())
	}
}

// resolveAll resolves every reference, refusing to act at all if any of them is
// unknown or ambiguous.
func resolveAll(refs []string) ([]recycler.Item, error) {
	items, err := recycler.List()
	if err != nil {
		return nil, err
	}
	resolved := make([]recycler.Item, 0, len(refs))
	for _, ref := range refs {
		item, err := resolve(items, ref)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, item)
	}
	return resolved, nil
}

// pathsEqual compares paths the way the local filesystem would.
func pathsEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if caseInsensitiveFilesystem {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func absOrSelf(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
