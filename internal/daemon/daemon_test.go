package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/diskfree"
)

// fakeBackend records what the sweep evicted without touching a filesystem.
type fakeBackend struct {
	evicted []string
	fail    map[string]error
}

func (f *fakeBackend) Recycle([]string) error                 { return nil }
func (f *fakeBackend) List() ([]bin.Item, error)              { return nil, nil }
func (f *fakeBackend) Restore(string, string) (string, error) { return "", nil }

func (f *fakeBackend) Evict(id string) error {
	if err, bad := f.fail[id]; bad {
		return err
	}
	f.evicted = append(f.evicted, id)
	return nil
}

// item builds a listing entry on the trash directory the tests probe.
func item(name string, size int64, ageHours int) bin.Item {
	return bin.Item{
		ID:        filepath.Join("/trash", "files", name),
		Name:      name,
		Size:      size,
		DeletedAt: time.Now().Add(-time.Duration(ageHours) * time.Hour),
	}
}

// freeSpace returns a probe reporting a fixed filesystem, so a test can put one
// under pressure without filling a real disk.
func freeSpace(avail, total uint64) func(string) (uint64, uint64, error) {
	return func(string) (uint64, uint64, error) { return avail, total, nil }
}

func TestFreeTargetIsATenthCappedAtAGigabyte(t *testing.T) {
	// A small filesystem keeps a tenth of itself.
	assert.Equal(t, uint64(100), FreeTarget(1000))
	// A large one stops at the ceiling rather than reserving a tenth of it.
	assert.Equal(t, uint64(freeTargetCeiling), FreeTarget(1<<40))
	// Exactly at the crossover the two agree.
	assert.Equal(t, uint64(freeTargetCeiling), FreeTarget(10*freeTargetCeiling))
}

func TestASweepLeavesAFilesystemWithRoomAlone(t *testing.T) {
	b := &fakeBackend{}
	items := []bin.Item{item("old.txt", 500, 48), item("new.txt", 500, 1)}

	evicted, err := sweepItems(b, items, freeSpace(1000, 2000))
	require.NoError(t, err)
	assert.Empty(t, evicted)
	assert.Empty(t, b.evicted, "nothing may go while there is room")
}

func TestASweepTakesTheOldestFirstAndStopsAtTheTarget(t *testing.T) {
	b := &fakeBackend{}
	// A 2000-byte filesystem wants 200 free and has 50. Freeing the oldest
	// 200-byte item alone clears the target, so the newer ones stay.
	items := []bin.Item{
		item("newest.txt", 200, 1),
		item("oldest.txt", 200, 72),
		item("middle.txt", 200, 24),
	}

	evicted, err := sweepItems(b, items, freeSpace(50, 2000))
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join("/trash", "files", "oldest.txt")}, b.evicted)
	require.Len(t, evicted, 1)
	assert.Equal(t, "oldest.txt", evicted[0].Item.Name)
	assert.NoError(t, evicted[0].Error)
}

func TestASweepKeepsGoingUntilTheTargetIsMet(t *testing.T) {
	b := &fakeBackend{}
	items := []bin.Item{
		item("a.txt", 100, 72),
		item("b.txt", 100, 48),
		item("c.txt", 100, 1),
	}

	// Wants 200 free of 2000 and has none, so two items go and the newest
	// survives.
	_, err := sweepItems(b, items, freeSpace(0, 2000))
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join("/trash", "files", "a.txt"),
		filepath.Join("/trash", "files", "b.txt"),
	}, b.evicted)
}

// An item of unknown size cannot be accounted for, so evicting it would be
// destroying something to reclaim a quantity the sweep cannot name.
func TestAnItemOfUnknownSizeIsNeverEvicted(t *testing.T) {
	b := &fakeBackend{}
	items := []bin.Item{
		item("mystery.txt", bin.SizeUnknown, 96),
		item("known.txt", 300, 1),
	}

	_, err := sweepItems(b, items, freeSpace(0, 2000))
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join("/trash", "files", "known.txt")}, b.evicted,
		"the unsized item must survive even though it is older")
}

// A failed eviction frees nothing, so the sweep must not count its bytes and
// stop short of the target believing it got them.
func TestAFailedEvictionDoesNotCountAsSpaceReclaimed(t *testing.T) {
	stuck := filepath.Join("/trash", "files", "stuck.txt")
	b := &fakeBackend{fail: map[string]error{stuck: errors.New("permission denied")}}
	items := []bin.Item{
		item("stuck.txt", 200, 72),
		item("next.txt", 200, 24),
	}

	evicted, err := sweepItems(b, items, freeSpace(0, 2000))
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join("/trash", "files", "next.txt")}, b.evicted)
	require.Len(t, evicted, 2)
	assert.Error(t, evicted[0].Error, "the failure is reported, not swallowed")
	assert.NoError(t, evicted[1].Error)
}

// Each filesystem is judged on its own free space: a full one must not reach
// across and evict items sitting on a different disk.
func TestEachFilesystemIsSweptOnItsOwnPressure(t *testing.T) {
	b := &fakeBackend{}
	items := []bin.Item{
		{ID: "/trash/files/a.txt", Name: "a.txt", Size: 200, DeletedAt: time.Now()},
		{ID: "/other/files/b.txt", Name: "b.txt", Size: 200, DeletedAt: time.Now()},
	}
	free := func(probe string) (uint64, uint64, error) {
		if probe == "/trash/files" {
			return 0, 2000, nil // full
		}
		return 2000, 2000, nil // empty
	}

	_, err := sweepItems(b, items, free)
	require.NoError(t, err)
	assert.Equal(t, []string{"/trash/files/a.txt"}, b.evicted)
}

// A filesystem that cannot be read is skipped, the same way an unreadable
// trash directory is skipped when listing: one bad mount must not stop the
// daemon guarding the rest.
func TestAnUnreadableFilesystemIsSkipped(t *testing.T) {
	b := &fakeBackend{}
	items := []bin.Item{item("a.txt", 200, 1)}

	evicted, err := sweepItems(b, items, func(string) (uint64, uint64, error) {
		return 0, 0, errors.New("no such filesystem")
	})
	require.NoError(t, err)
	assert.Empty(t, evicted)
	assert.Empty(t, b.evicted)
}

// The lock is what makes it one daemon per user. A second one has to find the
// lock held and stand down rather than sweep alongside the first.
func TestASecondDaemonStandsDown(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	lock, err := LockPath()
	require.NoError(t, err)

	unlock, held, err := tryLock(lock)
	require.NoError(t, err)
	require.True(t, held)
	defer unlock()

	_, held2, err := tryLock(lock)
	require.NoError(t, err)
	assert.False(t, held2, "two daemons took the same lock")

	assert.ErrorIs(t, Run(t.Context(), time.Second, nil), bin.ErrDaemonRunning)
}

// The loop is what makes it a daemon: it sweeps, reports, waits for the tick
// and sweeps again, until the context is done. A zero interval takes the
// default rather than spinning.
func TestTheDaemonSweepsUntilItsContextIsDone(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sweeps := 0
	err := Run(ctx, time.Millisecond, func(evicted []Eviction, err error) {
		assert.NoError(t, err)
		assert.Empty(t, evicted, "an empty bin has nothing to give back")
		if sweeps++; sweeps == 2 {
			cancel()
		}
	})
	assert.ErrorIs(t, err, context.Canceled)
	assert.GreaterOrEqual(t, sweeps, 2, "the daemon stopped after its first sweep")
}

// A zero interval is the caller not choosing one, not a request to spin.
func TestAZeroIntervalTakesTheDefault(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// The first sweep runs before the first tick, so cancelling from it returns
	// without ever waiting out DefaultPollInterval.
	err := Run(ctx, 0, func([]Eviction, error) { cancel() })
	assert.ErrorIs(t, err, context.Canceled)
}

// diskfree.Free is what the daemon polls, so it has to answer for a real path.
func TestDiskFreeReportsARealFilesystem(t *testing.T) {
	avail, total, err := diskfree.Free(t.TempDir())
	require.NoError(t, err)
	assert.Positive(t, total)
	assert.LessOrEqual(t, avail, total)
}

// Ensure must refuse a test binary. os.Executable() under `go test` is the test
// binary, and running one with a "daemon" argument re-runs the whole suite,
// which recycles, which lands back here: a fork bomb.
func TestTheDaemonIsNeverStartedFromATestBinary(t *testing.T) {
	started, err := Ensure(filepath.Join(t.TempDir(), "recycler.test"))
	assert.False(t, started)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test binary")
}
