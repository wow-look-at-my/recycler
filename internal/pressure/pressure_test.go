package pressure

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// The floor here and the daemon's target are the same number by intent: a
// single decides when recycling stops deferring anything, the other when a
// sweep starts. They are spelled again because the daemon imports the
// backends that call this, so this package cannot import it back.
func TestTheFloorMatchesTheDaemonsTarget(t *testing.T) {
	assert.Equal(t, uint64(1<<30), Floor(10<<30))
	assert.Equal(t, uint64(1<<30), Floor(1<<40))

	// A smaller a single keeps the fraction, so small media does not
	// become a permanent-delete device.
	assert.Equal(t, uint64(50<<20), Floor(500<<20))
	assert.Equal(t, uint64(100), Floor(1000))
}

const terabyte = uint64(1) << 40

func TestRoomToDeferMeansOrdinaryRecycling(t *testing.T) {
	d := decide(4<<30, terabyte, 1<<20)
	assert.False(t, d.Permanent)
	assert.Empty(t, d.Reason)
}

// Below the floor the bin cannot help: it is on the same filesystem, so the move
// frees nothing and the record of the move costs a little more.
func TestBelowTheFloorADeletionIsPermanent(t *testing.T) {
	d := decide(512<<20, terabyte, 1<<20)
	assert.True(t, d.Permanent)
	assert.Contains(t, d.Reason, "under the")
}

// An item bigger than what is left cannot be deferred at all.
func TestAnItemLargerThanTheSpaceLeftIsDeletedOutright(t *testing.T) {
	d := decide(4<<30, terabyte, 8<<30)
	assert.True(t, d.Permanent)
	assert.Contains(t, d.Reason, "does not fit")
}

// Both states are separate. An item that fits, on a filesystem with room, is
// recycled however large it is.
func TestAnItemThatFitsIsRecycled(t *testing.T) {
	d := decide(8<<30, terabyte, 4<<30)
	assert.False(t, d.Permanent)
}

// Guessing "permanent" from a size nobody could measure would destroy a file
// over a missing answer.
func TestAnUnmeasurableItemIsNotDeletedOnSizeAlone(t *testing.T) {
	d := decide(4<<30, terabyte, bin.SizeUnknown)
	assert.False(t, d.Permanent)
}

// A filesystem nothing can read leaves the decision where it was.
func TestAnUnreadableFilesystemRecyclesNormally(t *testing.T) {
	d := Check(string([]byte{0}), 1<<20)
	assert.False(t, d.Permanent)
}
