package pressure

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// The floor here and the daemon's target are the same number by intent. A
// single decides when recycling stops deferring anything, the other when a
// sweep starts.
func TestTheFloorScalesWithTheFilesystem(t *testing.T) {
	assert.Equal(t, uint64(50_000_000_000), Floor(2_000_000_000_000), "2 TB keeps 50 GB")
	assert.Equal(t, uint64(1000), Floor(40_000))
	assert.Equal(t, uint64(100<<30), Floor(4000<<30))

	// Large filesystems stop at the ceiling.
	assert.Equal(t, uint64(floorCeiling), Floor(floorCeiling*floorFraction))
	assert.Equal(t, uint64(floorCeiling), Floor(100<<40))
}

const terabyte = uint64(1) << 40

func TestRoomToDeferMeansOrdinaryRecycling(t *testing.T) {
	d := decide(4<<30, terabyte)
	assert.False(t, d.Permanent)
	assert.Empty(t, d.Reason)
}

// Below the floor the bin cannot help: it is on the same filesystem. The move
// frees nothing and the record of the move costs a little more.
func TestBelowTheFloorADeletionIsPermanent(t *testing.T) {
	d := decide(512<<20, terabyte)
	assert.True(t, d.Permanent)
	assert.Contains(t, d.Reason, "under the")
}

// A copy onto another filesystem bigger than what is left there cannot be
// deferred at all.
func TestACopyLargerThanTheSpaceLeftIsDeletedOutright(t *testing.T) {
	d := fits(decide(4<<30, terabyte), 4<<30, 8<<30)
	assert.True(t, d.Permanent)
	assert.Contains(t, d.Reason, "does not fit")
}

// Both states are separate. A copy that fits, onto a filesystem with room, is
// recycled however large it is.
func TestACopyThatFitsIsRecycled(t *testing.T) {
	d := fits(decide(8<<30, terabyte), 8<<30, 4<<30)
	assert.False(t, d.Permanent)
}

// Guessing "permanent" from a size nobody could measure would destroy a file
// over a missing answer.
func TestAnUnmeasurableItemIsNotDeletedOnSizeAlone(t *testing.T) {
	d := fits(decide(4<<30, terabyte), 4<<30, bin.SizeUnknown)
	assert.False(t, d.Permanent)
}

// A filesystem nothing can read leaves the decision where it was.
func TestAnUnreadableFilesystemRecyclesNormally(t *testing.T) {
	d := Check(string([]byte{0}), string([]byte{0}))
	assert.False(t, d.Permanent)
}
