// Package recycler provides a uniform API for the operating system's recycle
// bin (Windows), Trash (macOS) or trash can (Linux and other FreeDesktop
// systems).
//
// Every platform is reached through the same five operations: [Recycle],
// [List], [Restore], [Purge] and [Empty]. Items are addressed by an opaque
// [Item.ID] obtained from [List]; IDs are stable for as long as the item stays
// in the bin, but are not meaningful across platforms and must not be
// constructed by hand.
//
// Platform behaviour differs in ways the API cannot hide; those differences are
// documented on each function and summarised in the package README.
package recycler

import (
	"errors"
	"fmt"
	"time"
)

// Errors reported by this package. Use [errors.Is] to test for them; the
// returned errors are usually wrapped with additional context.
var (
	// ErrUnsupported is returned on platforms with no recycle bin support.
	ErrUnsupported = errors.New("recycler: platform not supported")

	// ErrNotFound is returned when no item in the recycle bin has the
	// requested ID.
	ErrNotFound = errors.New("recycler: item not found in recycle bin")

	// ErrUnknownOrigin is returned by Restore when the item's original
	// location is not recorded and no explicit destination was given.
	ErrUnknownOrigin = errors.New("recycler: original location unknown")

	// ErrExists is returned when a restore would overwrite an existing file.
	ErrExists = errors.New("recycler: destination already exists")
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

// backend is the per-platform implementation of the package API.
type backend interface {
	recycle(paths []string) error
	list() ([]Item, error)
	restore(id, dest string) (string, error)
	purge(ids []string) error
	empty() error
}

// Available reports whether this platform has a recycle bin implementation.
// When it returns false every other function in the package fails with
// [ErrUnsupported].
func Available() bool {
	_, err := platformBackend()
	return err == nil
}

// Recycle moves each path to the recycle bin. Directories are recycled whole.
// Errors for individual paths are collected and returned together, so a failure
// on one path does not prevent the others from being recycled.
//
// On Windows this defers to the shell, which - exactly as when deleting from
// Explorer - permanently deletes the file if the volume has no usable recycle
// bin (for example a network share, or a bin that is disabled or full).
func Recycle(paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	b, err := platformBackend()
	if err != nil {
		return err
	}
	return b.recycle(paths)
}

// List returns everything currently in the recycle bin, newest first.
//
// Only bins belonging to the current user are reported. Locations that cannot
// be read - an unreadable per-volume trash directory, say - are skipped rather
// than failing the whole listing.
func List() ([]Item, error) {
	b, err := platformBackend()
	if err != nil {
		return nil, err
	}
	return b.list()
}

// Get returns the item with the given ID, or [ErrNotFound].
func Get(id string) (Item, error) {
	items, err := List()
	if err != nil {
		return Item{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return Item{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Restore moves the item back to the location it was recycled from and returns
// that path. It fails with [ErrUnknownOrigin] if the original location is not
// known, and with [ErrExists] if something is already there.
func Restore(id string) (string, error) {
	return RestoreTo(id, "")
}

// RestoreTo moves the item out of the recycle bin to dest and returns the path
// it was restored to. An empty dest means the item's original location, making
// it equivalent to [Restore]. Missing parent directories of dest are created.
func RestoreTo(id, dest string) (string, error) {
	b, err := platformBackend()
	if err != nil {
		return "", err
	}
	return b.restore(id, dest)
}

// Purge permanently deletes the given items from the recycle bin. Errors for
// individual items are collected and returned together.
func Purge(ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	b, err := platformBackend()
	if err != nil {
		return err
	}
	return b.purge(ids)
}

// Empty permanently deletes everything in the current user's recycle bin.
func Empty() error {
	b, err := platformBackend()
	if err != nil {
		return err
	}
	return b.empty()
}
