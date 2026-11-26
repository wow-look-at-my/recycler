package pressure

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestBytesPicksTheUnit(t *testing.T) {
	assert.Equal(t, "512 B", Bytes(512))
	assert.Equal(t, "1.5 KiB", Bytes(1536))
	assert.Equal(t, "2.0 GiB", Bytes(2<<30))
}
