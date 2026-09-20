package diskfree

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A filesystem with no reserve reports every block as reachable.
func TestWithNoReserveEveryBlockIsReachable(t *testing.T) {
	assert.Equal(t, uint64(1000), reachable(int64(1000), int64(400), int64(400)))
}

// These are real numbers off a host that reserves most of the device for another
// user.
func TestAReserveForAnotherUserIsNotPartOfTheTotal(t *testing.T) {
	const (
		blocks = int64(66053021)
		free   = int64(64016139)
		avail  = int64(7675249)
	)
	got := reachable(blocks, free, avail)

	assert.Equal(t, uint64(blocks-(free-avail)), got)
	assert.Less(t, got, uint64(blocks), "the reserve has to come off the total")
	assert.GreaterOrEqual(t, got, uint64(avail), "what is available is reachable by definition")
}

// A reserve larger than the device is not a number to subtract into a wrap.
func TestAnImpossibleReserveReadsAsNothingReachable(t *testing.T) {
	assert.Equal(t, uint64(0), reachable(int64(10), int64(100), int64(0)))
}

func TestAvailableAboveFreeReadsAsNoReserve(t *testing.T) {
	assert.Equal(t, uint64(1000), reachable(int64(1000), int64(100), int64(200)))
}

// Unsigned platforms spell the same fields without a sign.
func TestUnsignedFieldsWorkTheSameWay(t *testing.T) {
	assert.Equal(t, uint64(600), reachable(uint64(1000), uint64(500), uint64(100)))
}

// Free has to answer for a real filesystem, and never report more reachable than
// the device holds.
func TestFreeReportsARealFilesystem(t *testing.T) {
	avail, total, err := Free(t.TempDir())
	require.NoError(t, err)
	assert.Positive(t, total)
	assert.LessOrEqual(t, avail, total)
}
