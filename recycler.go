package recycler

import (
	"context"
	"fmt"
	"time"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/daemon"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

var (
	ErrUnsupported = bin.ErrUnsupported

	ErrNotFound = bin.ErrNotFound

	ErrUnknownOrigin = bin.ErrUnknownOrigin

	ErrExists = bin.ErrExists

	// ErrDaemonRunning is returned.
	ErrDaemonRunning = bin.ErrDaemonRunning
)

const SizeUnknown = bin.SizeUnknown

type Item = bin.Item

// An Eviction records an item.
type Eviction = daemon.Eviction

// DefaultPollInterval is how often the daemon reads.
const DefaultPollInterval = daemon.DefaultPollInterval

// Available reports whether this platform has a recycle.
func Available() bool {
	_, err := trash.Backend()
	return err == nil
}

// Recycle moves each path to the recycle bin.
func Recycle(paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	b, err := trash.Backend()
	if err != nil {
		return err
	}
	return b.Recycle(paths)
}

// List returns everything currently in the recycle bin, newest at the front.
func List() ([]Item, error) {
	b, err := trash.Backend()
	if err != nil {
		return nil, err
	}
	return b.List()
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

// Restore moves the item back to the location.
func Restore(id string) (string, error) {
	return RestoreTo(id, "")
}

// RestoreTo moves the item out of the recycle bin to dest and returns the path it was restored to.
func RestoreTo(id, dest string) (string, error) {
	b, err := trash.Backend()
	if err != nil {
		return "", err
	}
	return b.Restore(id, dest)
}

// FreeTarget returns the number of available bytes the daemon keeps.
func FreeTarget(total uint64) uint64 { return daemon.FreeTarget(total) }

// Sweep reclaims space on every filesystem holding.
func Sweep() ([]Eviction, error) { return daemon.Sweep() }

// RunDaemon sweeps every interval until ctx is done.
func RunDaemon(ctx context.Context, interval time.Duration, report func([]Eviction, error)) error {
	return daemon.Run(ctx, interval, report)
}

// DaemonLockPath returns the file whose lock names the running.
func DaemonLockPath() (string, error) { return daemon.LockPath() }

// EnsureDaemon starts the disk-pressure daemon if none is already.
func EnsureDaemon(exe string) (bool, error) { return daemon.Ensure(exe) }
