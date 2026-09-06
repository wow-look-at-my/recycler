package bin

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

var (
	// ErrUnsupported is returned on platforms with no recycle bin.
	ErrUnsupported = errors.New("recycler: platform not supported")

	// ErrNotFound is returned when no item in the recycle bin.
	ErrNotFound = errors.New("recycler: item not found in recycle bin")

	// ErrUnknownOrigin is returned by a restore when the item's.
	ErrUnknownOrigin = errors.New("recycler: original location unknown")

	// ErrExists is returned when a restore would overwrite.
	ErrExists = errors.New("recycler: destination already exists")

	// ErrDaemonRunning is returned when another daemon already holds.
	ErrDaemonRunning = errors.New("recycler: a daemon is already running")
)

const SizeUnknown int64 = -1

type Item struct {
	ID string `json:"id"`

	Name string `json:"name"`

	// OriginalPath is the absolute path the item.
	OriginalPath string `json:"original_path,omitempty"`

	DeletedAt time.Time `json:"deleted_at"`

	Size int64 `json:"size"`

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

// Sort puts the newest item at the front, with the ID as a tie-breaker so listings are
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
type Backend interface {
	Recycle(paths []string) error
	List() ([]Item, error)
	Restore(id, dest string) (string, error)
	Evict(id string) error
}
