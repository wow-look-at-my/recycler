package daemon

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// A sweep that derives the filesystems it reads from the listing alone makes
// empty probes on an empty bin, and exits clean and silent while the disk is
// full. A daemon that says nothing then reads as a single keeping up.
func TestAFilesystemIsReadEvenWhenItsBinIsEmpty(t *testing.T) {
	b := &fakeBackend{}
	probed := 0
	free := func(string) (uint64, uint64, error) {
		probed++
		return 0, 2000, nil
	}

	evicted, pressures, err := sweepItems(b, nil, dirs, free)
	require.NoError(t, err)
	assert.Positive(t, probed, "an empty bin left the filesystem unread")
	assert.Empty(t, evicted, "an empty bin has nothing to give back")

	require.Len(t, pressures, 1, "a full filesystem it cannot help must still be reported")
	assert.Equal(t, uint64(0), pressures[0].Avail)
	assert.Equal(t, FreeTarget(2000), pressures[0].Target)
	assert.Equal(t, FreeTarget(2000), pressures[0].Shortfall())
	assert.Zero(t, pressures[0].Stranded, "nothing was in the bin to strand")
}

// Live files are what the daemon cannot touch. Reporting the shortfall is the
// only thing it can do about them, and doing it is the point.
func TestPressureIsReportedWhenNothingRecycledIsLeftToGiveBack(t *testing.T) {
	b := &fakeBackend{}
	// A single small item against a filesystem far below its target.
	items := []bin.Item{item("crumb.txt", 10, 72)}

	evicted, pressures, err := sweepItems(b, items, dirs, freeSpace(0, 20000))
	require.NoError(t, err)
	require.Len(t, evicted, 1, "it gives back what it has")

	require.Len(t, pressures, 1)
	assert.Equal(t, uint64(10), pressures[0].Avail, "the sweep freed only the crumb")
	assert.Equal(t, FreeTarget(20000)-10, pressures[0].Shortfall())
}

// A filesystem the sweep brought back above its trigger is not pressure. Saying
// so every tick would bury the report that matters.
func TestAFilesystemBroughtBackAboveItsTriggerIsNotReported(t *testing.T) {
	b := &fakeBackend{}
	items := []bin.Item{item("big.bin", 1000, 72)}

	_, pressures, err := sweepItems(b, items, dirs, freeSpace(0, 2000))
	require.NoError(t, err)
	assert.Empty(t, pressures, "a sweep that cleared the trigger has nothing to complain about")
}

// Falling short of the runway is ordinary. Only dropping under the trigger is
// trouble, so that is what a report is measured against.
func TestFallingShortOfTheRunwayAloneIsNotPressure(t *testing.T) {
	b := &fakeBackend{}
	items := []bin.Item{item("some.bin", 250, 72)}

	_, pressures, err := sweepItems(b, items, dirs, freeSpace(0, 2000))
	require.NoError(t, err)
	require.Greater(t, RecoverTarget(2000), uint64(250))
	assert.Empty(t, pressures, "reaching the trigger is enough to stop complaining")
}

// An eviction that failed freed nothing, so its bytes are still sitting there.
// Naming them separates "there is nothing left" from "it would not go".
func TestPressureCountsWhatWouldNotBeEvicted(t *testing.T) {
	stuck := filepath.Join("/trash", "files", "stuck.bin")
	b := &fakeBackend{fail: map[string]error{stuck: errors.New("permission denied")}}
	items := []bin.Item{item("stuck.bin", 5000, 72)}

	_, pressures, err := sweepItems(b, items, dirs, freeSpace(0, 20000))
	require.NoError(t, err)
	require.Len(t, pressures, 1)
	assert.Equal(t, uint64(5000), pressures[0].Stranded,
		"bytes that would not go are not the same as bytes that are not there")
}

// A bin directory nothing has been recycled into yet does not exist. Refusing to
// measure its filesystem would leave the daemon blind until the earliest
// recycle, which is the window a machine fills the disk in.
func TestAProbeFallsBackToAnExistingAncestor(t *testing.T) {
	existing := t.TempDir()
	missing := filepath.Join(existing, "Trash", "files")

	assert.Equal(t, existing, probePath(missing))
	assert.Equal(t, existing, probePath(existing))
}
