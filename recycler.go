// Package recycler provides a uniform API for the operating system's recycle
// bin (Windows), Trash (macOS) or trash can (Linux and other FreeDesktop
// systems).
//
// Every platform is reached through the same three operations: [Recycle],
// [List] and [Restore]. Items are addressed by an opaque [Item.ID] obtained
// from [List]; IDs are stable for as long as the item stays in the bin, but are
// not meaningful across platforms and must not be constructed by hand.
//
// Nothing here lets a caller delete anything permanently. Recycling is
// reversible and everything a user can reach keeps it that way: an item leaves
// the bin by being restored. Emptying the bin is the desktop environment's job.
// Do not add a purge or empty operation.
//
// The one exception is disk pressure, and it is not an operation a caller
// names. Deferring a deletion only works while there is room to defer it into,
// so [Sweep] gives back the oldest items when the filesystem holding a bin runs
// low. Pressure decides what goes, never a command.
//
// Platform behaviour differs in ways the API cannot hide; those differences are
// documented on each function and summarised in the package README.
//
// This file is the whole public surface. The implementation lives under
// internal: the platform backends in internal/trash, the disk-pressure daemon
// in internal/daemon, and the types both speak in internal/bin.
package recycler

import (
	"context"
	"fmt"
	"time"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/daemon"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

// Errors reported by this package. Use [errors.Is] to test for them; the
// returned errors are usually wrapped with additional context.
var (
	// ErrUnsupported is returned on platforms with no recycle bin support.
	ErrUnsupported = bin.ErrUnsupported

	// ErrNotFound is returned when no item in the recycle bin has the
	// requested ID.
	ErrNotFound = bin.ErrNotFound

	// ErrUnknownOrigin is returned by Restore when the item's original
	// location is not recorded and no explicit destination was given.
	ErrUnknownOrigin = bin.ErrUnknownOrigin

	// ErrExists is returned when a restore would overwrite an existing file.
	ErrExists = bin.ErrExists

	// ErrDaemonRunning is returned by RunDaemon when another daemon already
	// holds the lock.
	ErrDaemonRunning = bin.ErrDaemonRunning
)

// SizeUnknown is reported in [Item.Size] when the platform does not record the
// size of a recycled item and it could not be determined.
const SizeUnknown = bin.SizeUnknown

// An Item is a single entry in the recycle bin.
type Item = bin.Item

// An Eviction records one item the disk-pressure daemon destroyed to reclaim
// space.
type Eviction = daemon.Eviction

// DefaultPollInterval is how often the daemon reads free space.
const DefaultPollInterval = daemon.DefaultPollInterval

// Available reports whether this platform has a recycle bin implementation.
// When it returns false every other function in the package fails with
// [ErrUnsupported].
func Available() bool {
	_, err := trash.Backend()
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
	b, err := trash.Backend()
	if err != nil {
		return err
	}
	return b.Recycle(paths)
}

// List returns everything currently in the recycle bin, newest first.
//
// Only bins belonging to the current user are reported. Locations that cannot
// be read - an unreadable per-volume trash directory, say - are skipped rather
// than failing the whole listing.
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
	b, err := trash.Backend()
	if err != nil {
		return "", err
	}
	return b.Restore(id, dest)
}

// FreeTarget returns the number of available bytes the daemon keeps on a
// filesystem of the given total size: min(10% of the filesystem, 1 GiB).
func FreeTarget(total uint64) uint64 { return daemon.FreeTarget(total) }

// Sweep reclaims space on every filesystem holding a recycle bin whose
// available space is under [FreeTarget], destroying the oldest items until it
// is met or the bin is empty. It returns what it evicted.
//
// Sizes come from what was recorded when each item was recycled. Nothing in the
// bin is walked or stat-ed to size it: the contents of a recycled item do not
// change, so the number taken at ingestion is still the number now, and a sweep
// that re-measured would walk the whole bin every poll.
//
// An item whose size was never recorded is left alone. It cannot be accounted
// for, and destroying something to reclaim an unknown quantity is not a trade
// this can make honestly.
func Sweep() ([]Eviction, error) { return daemon.Sweep() }

// RunDaemon sweeps every interval until ctx is done. A zero interval means
// [DefaultPollInterval]. Each sweep's evictions are handed to report, which may
// be nil. It returns [ErrDaemonRunning] when another daemon already holds the
// lock, so a second one started by hand stands down rather than sweeping in
// parallel.
func RunDaemon(ctx context.Context, interval time.Duration, report func([]Eviction, error)) error {
	return daemon.Run(ctx, interval, report)
}

// DaemonLockPath returns the file whose lock names the running daemon. One
// daemon holds it per user, which is what keeps a recycle every few seconds
// from starting a daemon every few seconds.
func DaemonLockPath() (string, error) { return daemon.LockPath() }

// EnsureDaemon starts the disk-pressure daemon if one is not already running,
// by running exe with the "daemon" argument detached from this process. It
// reports whether it started one.
//
// Spawning is the caller's decision rather than something [Recycle] does: this
// package is a library, and a library that forks a process out of an ordinary
// call is not something a program can be expected to want. The CLI calls this
// after recycling, which is what makes the daemon appear on first use of the
// tool.
func EnsureDaemon(exe string) (bool, error) { return daemon.Ensure(exe) }
