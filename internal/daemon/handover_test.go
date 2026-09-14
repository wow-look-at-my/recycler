package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/recycler/internal/bin"
)

// isolateDaemonState puts the lock, the identity record and the recycle bin
// itself under a temporary home, so a test never reaches the daemon or the bin
// the person running it has.
func isolateDaemonState(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "share"))
}

// buildID describes a build of the given version whose executable was written
// age ago.
func buildID(version string, age time.Duration) Identity {
	return Identity{
		Version: version,
		Exe:     filepath.Join("/nonexistent", "recycler-"+version),
		ModTime: time.Now().Add(-age).UTC(),
	}
}

// startDaemon runs one build and reports how many times it has swept.
func startDaemon(ctx context.Context, id Identity) (*atomic.Int64, <-chan error) {
	var sweeps atomic.Int64
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, time.Millisecond, id, func([]Eviction, []Pressure, error) {
			sweeps.Add(1)
		})
	}()
	return &sweeps, done
}

func requireRunning(t *testing.T, want Identity) {
	t.Helper()
	require.Eventually(t, func() bool {
		running, ok := Running()
		return ok && running.Exe == want.Exe && running.Version == want.Version
	}, 10*time.Second, 5*time.Millisecond, "the daemon holding the lock is not %s", want)
}

func requireSweeping(t *testing.T, sweeps *atomic.Int64) {
	t.Helper()
	from := sweeps.Load()
	require.Eventually(t, func() bool { return sweeps.Load() > from },
		10*time.Second, 5*time.Millisecond, "the daemon stopped sweeping")
}

func mustLockPath(t *testing.T) string {
	t.Helper()
	lock, err := LockPath()
	require.NoError(t, err)
	return lock
}

// shortenHandover keeps a test that waits out a handover from waiting the whole
// timeout a real machine gets.
func shortenHandover(t *testing.T, d time.Duration) {
	t.Helper()
	was := handoverTimeout
	handoverTimeout = d
	t.Cleanup(func() { handoverTimeout = was })
}

// An older build must not take the disk away from a newer one. The older build
// here has the more recently written executable, which the version outranks.
func TestAnOlderDaemonWillNotDisplaceANewerOne(t *testing.T) {
	isolateDaemonState(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	newer, older := buildID("2.0.0", time.Hour), buildID("1.9.0", 0)
	sweeps, done := startDaemon(ctx, newer)
	requireRunning(t, newer)

	err := run(t.Context(), time.Millisecond, older, nil)
	require.ErrorIs(t, err, bin.ErrDaemonRunning)
	assert.Contains(t, err.Error(), "is not newer")

	requireRunning(t, newer)
	requireSweeping(t, sweeps)

	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)
}

// A newer build takes the lock, and the older one gives it up rather than
// sweeping alongside it.
func TestANewerDaemonTakesOverFromAnOlderOne(t *testing.T) {
	isolateDaemonState(t)
	oldCtx, cancelOld := context.WithCancel(t.Context())
	defer cancelOld()
	newCtx, cancelNew := context.WithCancel(t.Context())
	defer cancelNew()

	older, newer := buildID("1.0.0", time.Hour), buildID("1.0.1", time.Hour)
	oldSweeps, oldDone := startDaemon(oldCtx, older)
	requireRunning(t, older)

	newSweeps, newDone := startDaemon(newCtx, newer)

	select {
	case err := <-oldDone:
		assert.NoError(t, err, "the older daemon did not stand down cleanly")
	case <-time.After(30 * time.Second):
		t.Fatal("the older daemon never stood down")
	}

	requireRunning(t, newer)
	requireSweeping(t, newSweeps)
	assert.False(t, lockFree(mustLockPath(t)), "nothing holds the daemon lock")

	stopped := oldSweeps.Load()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, stopped, oldSweeps.Load(), "the older daemon swept after handing over")

	cancelNew()
	assert.ErrorIs(t, <-newDone, context.Canceled)
}

// The same version is not newer, whatever the executables say.
func TestAnEqualBuildStandsDown(t *testing.T) {
	isolateDaemonState(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	running := buildID("1.4.2", time.Hour)
	sweeps, done := startDaemon(ctx, running)
	requireRunning(t, running)

	same := buildID("1.4.2", 0)
	err := run(t.Context(), time.Millisecond, same, nil)
	require.ErrorIs(t, err, bin.ErrDaemonRunning)
	assert.Contains(t, err.Error(), "is not newer")

	requireRunning(t, running)
	requireSweeping(t, sweeps)

	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)
}

// Without a version on both sides there is nothing to order but the
// executables, so the one written last is the newer build.
func TestUnversionedDaemonsAreOrderedByTheirExecutables(t *testing.T) {
	isolateDaemonState(t)
	oldCtx, cancelOld := context.WithCancel(t.Context())
	defer cancelOld()
	newCtx, cancelNew := context.WithCancel(t.Context())
	defer cancelNew()

	older := Identity{Exe: "/nonexistent/recycler-old", ModTime: time.Now().Add(-time.Hour).UTC()}
	newer := Identity{Exe: "/nonexistent/recycler-new", ModTime: time.Now().UTC()}

	_, oldDone := startDaemon(oldCtx, older)
	requireRunning(t, older)

	newSweeps, newDone := startDaemon(newCtx, newer)
	select {
	case err := <-oldDone:
		assert.NoError(t, err, "the older executable did not stand down cleanly")
	case <-time.After(30 * time.Second):
		t.Fatal("the older executable never stood down")
	}
	requireRunning(t, newer)
	requireSweeping(t, newSweeps)

	// And the one written first cannot take it back.
	err := run(t.Context(), time.Millisecond, older, nil)
	require.ErrorIs(t, err, bin.ErrDaemonRunning)
	requireRunning(t, newer)

	cancelNew()
	assert.ErrorIs(t, <-newDone, context.Canceled)
}

// A handover nobody completes leaves the disk watched: the daemon that offered
// the lock takes it back rather than exiting into a gap.
func TestADaemonResumesWhenTheSuccessorNeverArrives(t *testing.T) {
	isolateDaemonState(t)
	shortenHandover(t, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	running := buildID("1.0.0", time.Hour)
	sweeps, done := startDaemon(ctx, running)
	requireRunning(t, running)

	require.NoError(t, requestHandover(buildID("5.0.0", 0)))

	// The request going away is the daemon having offered the lock, waited out
	// the successor that never came, and taken it back.
	path, err := handoverPath()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return errors.Is(err, os.ErrNotExist)
	}, 10*time.Second, 5*time.Millisecond, "the abandoned request was never cleared")

	requireRunning(t, running)
	requireSweeping(t, sweeps)
	assert.False(t, lockFree(mustLockPath(t)), "nothing holds the daemon lock")

	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)
}

// A daemon that recorded no build is not something to compare against, so it
// keeps the lock.
func TestADaemonThatRecordedNoBuildIsLeftAlone(t *testing.T) {
	isolateDaemonState(t)
	lock := mustLockPath(t)
	unlock, held, err := tryLock(lock)
	require.NoError(t, err)
	require.True(t, held)
	defer unlock()

	err = run(t.Context(), time.Millisecond, buildID("9.9.9", 0), nil)
	require.ErrorIs(t, err, bin.ErrDaemonRunning)
	assert.Contains(t, err.Error(), "recorded no build")
}

// Ensure makes the same decision the daemon does, without starting anything it
// would have to stop again.
func TestEnsureLeavesANewerDaemonAlone(t *testing.T) {
	isolateDaemonState(t)
	was := Version
	Version = "1.0.0"
	t.Cleanup(func() { Version = was })

	lock := mustLockPath(t)
	unlock, held, err := tryLock(lock)
	require.NoError(t, err)
	require.True(t, held)
	defer unlock()

	withdraw := announce(buildID("2.0.0", time.Hour))
	defer withdraw()

	started, err := Ensure(filepath.Join(t.TempDir(), "recycler"))
	require.NoError(t, err)
	assert.False(t, started, "a daemon was started to replace a newer one")
}

// The record outlives nothing: it is written when the lock is taken and gone
// when it is released, so a reader never takes it for a daemon that is not
// there.
func TestTheIdentityRecordIsWithdrawnWhenTheDaemonStops(t *testing.T) {
	isolateDaemonState(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	me := buildID("1.0.0", time.Hour)
	_, done := startDaemon(ctx, me)
	requireRunning(t, me)

	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)

	_, ok := Running()
	assert.False(t, ok, "a stopped daemon left its build recorded")
}
