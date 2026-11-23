package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// fakeIndex is a listing the test changes by hand, as a directory watch would.
type fakeIndex struct {
	b         *listedBackend
	changed   []string
	refreshes int
	err       error
	closed    bool
}

func (x *fakeIndex) Refresh() ([]string, error) {
	x.refreshes++
	if x.err != nil {
		return nil, x.err
	}
	changed := x.changed
	x.changed = nil
	return changed, nil
}

func (x *fakeIndex) List() []bin.Item {
	var left []bin.Item
	for _, it := range x.b.items {
		gone := false
		for _, id := range x.b.evicted {
			gone = gone || id == it.ID
		}
		if !gone {
			left = append(left, it)
		}
	}
	return left
}

func (x *fakeIndex) Dirs() []string { return []string{x.b.dir} }
func (x *fakeIndex) Close() error   { x.closed = true; return nil }

// watchableBackend keeps a fakeIndex. Its own List counts a read of the bin.
type watchableBackend struct {
	*listedBackend
	index *fakeIndex
}

func (w watchableBackend) Watch() (bin.Index, error) { return w.index, nil }

func watchAWatchedBin(t *testing.T) (*watched, *fakeIndex) {
	t.Helper()
	w := watchABin(t)
	index := &fakeIndex{b: w.b}
	w.backend = func() (bin.Backend, error) { return watchableBackend{listedBackend: w.b, index: index}, nil }
	return w, index
}

// The watches see every change, so the daemon never reads the bin itself, and
// an unchanged bin is never swept again.
func TestAWatchedBinIsNeverRead(t *testing.T) {
	w, index := watchAWatchedBin(t)
	w.b.items = []bin.Item{w.b.recycled("old.bin", 500, 72)}

	w.after(t, 0)
	for range 3 {
		w.after(t, relistInterval)
	}
	assert.Zero(t, w.b.lists, "the daemon read a bin the watches keep")
	assert.Equal(t, 4, index.refreshes)
	assert.Equal(t, 1, w.sweeps(), "an unchanged watched bin was swept again")
}

// A change the watch reports waits for the interval, as a change time does.
func TestAWatchedChangeIsSweptOnceTheIntervalHasPassed(t *testing.T) {
	w, index := watchAWatchedBin(t)
	w.after(t, 0)

	index.changed = []string{w.b.dir}
	w.after(t, time.Second)
	assert.Equal(t, 1, w.sweeps(), "a change was swept before the interval passed")

	w.after(t, settleInterval)
	assert.Equal(t, 2, w.sweeps(), "a watched change was never swept")

	w.after(t, settleInterval)
	assert.Equal(t, 2, w.sweeps(), "the same change was swept twice")
}

// Pressure evicts what the index lists, with no read of the bin.
func TestPressureEvictsFromTheIndex(t *testing.T) {
	w, _ := watchAWatchedBin(t)
	w.b.items = []bin.Item{w.b.recycled("old.bin", 500, 72)}
	w.avail = 10

	evicted, _ := w.after(t, 0)
	require.Len(t, evicted, 1)
	assert.Equal(t, w.b.items[0].ID, evicted[0].Item.ID)
	assert.Zero(t, w.b.lists)
}

// A watch that fails is reported, and the bin is read again from then on.
func TestALostWatchFallsBackToReadingTheBin(t *testing.T) {
	w, index := watchAWatchedBin(t)
	w.after(t, 0)
	require.Zero(t, w.b.lists)

	index.err = errors.New("queue gone")
	w.after(t, time.Second)
	assert.True(t, index.closed)
	assert.Equal(t, 1, w.b.lists, "the bin was not read once its watch was lost")
}

// sweeps counts the sweeps the watch ran: each records when it listed.
func (w *watched) sweeps() int {
	return w.sweepCount
}
