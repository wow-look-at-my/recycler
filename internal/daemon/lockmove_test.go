package daemon

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/recycler/internal/bin"
)

// A daemon whose lock file is deleted takes the lock again at the same path, so
// a daemon started afterwards still finds it held.
func TestADaemonRetakesALockThatWasRemoved(t *testing.T) {
	isolateDaemonState(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	me := buildID("1.0.0", time.Hour)
	sweeps, done := startDaemon(ctx, me)
	requireRunning(t, me)

	lock := mustLockPath(t)
	require.NoError(t, os.Remove(lock))

	require.Eventually(t, func() bool {
		_, err := os.Stat(lock)
		_, known := Running()
		return err == nil && known
	}, 10*time.Second, 5*time.Millisecond, "the daemon never took the removed lock back")
	requireRunning(t, me)
	requireSweeping(t, sweeps)
	assert.False(t, lockFree(lock), "nothing holds the daemon lock")

	err := run(t.Context(), time.Millisecond, buildID("1.0.0", 0), false, nil)
	require.ErrorIs(t, err, bin.ErrDaemonRunning)

	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)
}

// A daemon whose lock file was replaced by one another daemon holds stands down
// rather than sweeping alongside it.
func TestADaemonStandsDownWhenItsLockIsReplaced(t *testing.T) {
	isolateDaemonState(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	me := buildID("1.0.0", time.Hour)
	_, done := startDaemon(ctx, me)
	requireRunning(t, me)

	lock := mustLockPath(t)
	replacement := lock + ".new"
	unlock, held, err := tryLock(replacement)
	require.NoError(t, err)
	require.True(t, held)
	defer unlock()
	other := buildID("1.0.0", 0)
	withdraw := announce(other)
	defer withdraw()
	require.NoError(t, os.Rename(replacement, lock))

	select {
	case err := <-done:
		require.ErrorIs(t, err, bin.ErrDaemonRunning)
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon kept sweeping after its lock was replaced")
	}
	requireRunning(t, other)
}

// A standby daemon waits while another build no older than it sweeps, and takes
// the lock when that one stops.
func TestAStandbyDaemonTakesOverWhenTheRunningOneStops(t *testing.T) {
	isolateDaemonState(t)
	firstCtx, stopFirst := context.WithCancel(t.Context())
	defer stopFirst()
	secondCtx, stopSecond := context.WithCancel(t.Context())
	defer stopSecond()

	first := buildID("1.0.0", time.Hour)
	_, firstDone := startDaemon(firstCtx, first)
	requireRunning(t, first)

	second := buildID("1.0.0", 0)
	secondSweeps, secondDone := startDaemonIn(secondCtx, second, true)
	select {
	case err := <-secondDone:
		t.Fatalf("the standby daemon returned instead of waiting: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	assert.Zero(t, secondSweeps.Load(), "the standby daemon swept alongside the running one")
	requireRunning(t, first)

	stopFirst()
	assert.ErrorIs(t, <-firstDone, context.Canceled)

	requireRunning(t, second)
	requireSweeping(t, secondSweeps)

	stopSecond()
	assert.ErrorIs(t, <-secondDone, context.Canceled)
}

// A completed handover is the recorded build being no older than the one that
// asked for it.
func TestWaitForSuccessorWatchesTheRecordedBuild(t *testing.T) {
	isolateDaemonState(t)
	me := buildID("2.0.0", time.Hour)
	assert.False(t, waitForSuccessor(me, 20*time.Millisecond), "nothing is recorded, so nothing took over")

	withdraw := announce(buildID("1.0.0", 0))
	assert.False(t, waitForSuccessor(me, 20*time.Millisecond), "an older build is not a successor")
	withdraw()

	withdraw = announce(buildID("2.0.0", 0))
	defer withdraw()
	assert.True(t, waitForSuccessor(me, time.Second))
}

// A standby daemon still waiting for the lock stops when its context does.
func TestAStandbyDaemonStopsWhileWaiting(t *testing.T) {
	isolateDaemonState(t)
	lock := mustLockPath(t)
	unlock, held, err := tryLock(lock)
	require.NoError(t, err)
	require.True(t, held)
	defer unlock()
	withdraw := announce(buildID("1.0.0", 0))
	defer withdraw()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err = run(ctx, time.Millisecond, buildID("1.0.0", time.Hour), true, nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
