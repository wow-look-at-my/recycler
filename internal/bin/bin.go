// Package bin holds what every part of the recycle bin agrees on: the item a
// listing is made of, the errors the API reports, and the interface a platform
// backend satisfies.
//
// It exists so that the backends, the filesystem helpers and the disk-pressure
// daemon can all name these without importing each other. Nothing here touches
// a disk.
package bin

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Errors reported by this package's callers. Use [errors.Is] to test for them;
// the returned errors are usually wrapped with additional context.
var (
	// ErrUnsupported is returned on platforms with no recycle bin support.
	ErrUnsupported = errors.New("recycler: platform not supported")

	// ErrNotFound is returned when no item in the recycle bin has the
	// requested ID.
	ErrNotFound = errors.New("recycler: item not found in recycle bin")

	// ErrUnknownOrigin is returned by a restore when the item's original
	// location is not recorded and no explicit destination was given.
	ErrUnknownOrigin = errors.New("recycler: original location unknown")

	// ErrExists is returned when a restore would overwrite an existing file.
	ErrExists = errors.New("recycler: destination already exists")

	// ErrDaemonRunning is returned when another daemon already holds the lock.
	ErrDaemonRunning = errors.New("recycler: a daemon is already running")
)

// SizeUnknown is reported in [Item.Size] when the platform does not record the
// size of a recycled item and it could not be determined.
const SizeUnknown int64 = -1

// An Item is a single entry in the recycle bin.
type Item struct {
	// ID is an opaque, platform-specific handle for the item. It is stable
	// while the item remains in the recycle bin.
	ID string `json:"id"`

	// Name is the display name of the item, normally the base name of the
	// file at the time it was recycled.
	Name string `json:"name"`

	// OriginalPath is the absolute path the item was recycled from. It is
	// empty when nothing recorded where the item came from: on macOS, where
	// the location lives in Finder's own put back records, that means an item
	// trashed by a tool that writes none.
	OriginalPath string `json:"original_path,omitempty"`

	// DeletedAt is when the item was moved to the recycle bin. When the
	// platform records no deletion time - macOS does not - a timestamp of the
	// recycled copy is used instead.
	DeletedAt time.Time `json:"deleted_at"`

	// Size is the size of the item in bytes: the total size of its contents
	// for a directory, or [SizeUnknown] if it could not be determined.
	Size int64 `json:"size"`

	// IsDir reports whether the item is a directory.
	IsDir bool `json:"is_dir"`
}

// String returns a short human-readable description of the item.
func (i Item) String() string {
	where := i.OriginalPath
	if where == "" {
		where = i.Name + " (original location unknown)"
	}
	return fmt.Sprintf("%s [%s]", where, i.DeletedAt.Format(time.RFC3339))
}

// Sort orders items newest first, with the ID as a tie-breaker so listings are
// stable.
func Sort(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].DeletedAt.Equal(items[j].DeletedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].DeletedAt.After(items[j].DeletedAt)
	})
}

// A Backend is the per-platform implementation of the package API.
//
// Evict is the only method that destroys anything, and exists only for the
// disk-pressure daemon. It must validate its ID exactly as Restore does, so a
// malformed one can never reach a file outside the bin.
type Backend interface {
	Recycle(paths []string) error
	List() ([]Item, error)
	Restore(id, dest string) (string, error)
	Evict(id string) error
}
