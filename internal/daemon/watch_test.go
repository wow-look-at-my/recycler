package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// listedBackend counts the listings a watch asks for. Its bin is a real
// directory, so the change times the watch reads are real.
type listedBackend struct {
	fakeBackend
	dir     string
	items   []bin.Item
	lists   int
	listErr error
}

func (l *listedBackend) Dirs() []string { return []string{l.dir} }

func (l *listedBackend) List() ([]bin.Item, error) {
	l.lists++
	if l.listErr != nil {
		return nil, l.listErr
	}
	var left []bin.Item
	for _, it := range l.items {
		gone := false
		for _, id := range l.evicted {
			gone = gone || id == it.ID
		}
		if !gone {
			left = append(left, it)
		}
	}
	return left, nil
}

// recycled is an item in the bin of b.
func (l *listedBackend) recycled(name string, size int64, ageHours int) bin.Item {
	it := item(name, size, ageHours)
	it.ID = filepath.Join(l.dir, "files", name)
	return it
}

// watched is a watch over one bin, with a clock and a free-space reading the
// test moves by hand.
type watched struct {
	*watch
	b     *listedBackend
	clock time.Time
	avail uint64
	total uint64
}

// watchABin builds a bin whose directories last changed an hour ago, on a
// filesystem with room.
func watchABin(t *testing.T) *watched {
	t.Helper()
	dir := t.TempDir()
	w := &watched{b: &listedBackend{dir: dir}, clock: time.Now(), avail: 1000, total: 2000}
	for _, sub := range []string{"files", "info"} {
		require.NoError(t, os.Mkdir(filepath.Join(dir, sub), 0o700))
	}
	w.age(t)
	w.watch = newWatch()
	w.backend = func() (bin.Backend, error) { return w.b, nil }
	w.free = func(string) (uint64, uint64, error) { return w.avail, w.total, nil }
	w.now = func() time.Time { return w.clock }
	settings := filepath.Join(t.TempDir(), "settings.json")
	w.settings = &settingsCache{path: func() (string, error) { return settings, nil }}
	return w
}

// A running daemon takes a new free-space goal on its next tick, with no restart.
func TestARunningDaemonReadsANewMinFree(t *testing.T) {
	w := watchABin(t)
	w.total = 1 << 40
	w.avail = 5 << 30
	_, pressures := w.after(t, time.Second)
	assert.Empty(t, pressures, "5 GiB free is over the built-in 1 GiB trigger")

	path, err := w.settings.path()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(`{"min_free": "10GiB"}`), 0o600))
	_, pressures = w.after(t, time.Second)
	require.Len(t, pressures, 1, "5 GiB free is under a 10 GiB goal")
	assert.Equal(t, uint64(10<<30), pressures[0].Target)

	// A file that cannot be read gives the built-in goal back.
	require.NoError(t, os.WriteFile(path, []byte(`{"min_free": "lots"}`), 0o600))
	_, pressures = w.after(t, time.Second)
	assert.Empty(t, pressures)
}

// age moves the change time of every bin directory an hour back.
func (w *watched) age(t *testing.T) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	for _, sub := range []string{"", "files", "info"} {
		require.NoError(t, os.Chtimes(filepath.Join(w.b.dir, sub), old, old))
	}
}

// after moves the clock and runs one tick.
func (w *watched) after(t *testing.T, d time.Duration) ([]Eviction, []Pressure) {
	t.Helper()
	w.clock = w.clock.Add(d)
	evicted, pressures, err := w.tick()
	require.NoError(t, err)
	return evicted, pressures
}

// recycle puts an entry in the bin, which moves the change time of files/.
func (w *watched) recycle(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(w.b.dir, "files", name), nil, 0o600))
}

// A listing reads every recycled item. A bin nobody touched, on a filesystem
// with room, has nothing new to say on the next tick.
func TestAnUnchangedBinIsNotListedOnEveryTick(t *testing.T) {
	w := watchABin(t)

	w.after(t, 0)
	require.Equal(t, 1, w.b.lists, "the first tick has to list")

	for range 5 {
		w.after(t, time.Second)
	}
	w.after(t, settleInterval)
	assert.Equal(t, 1, w.b.lists, "an unchanged bin with room was listed again")
}

// The free space is the reading that cannot wait.
func TestEveryTickReadsFreeSpace(t *testing.T) {
	w := watchABin(t)
	probes := 0
	w.free = func(string) (uint64, uint64, error) {
		probes++
		return w.avail, w.total, nil
	}

	w.after(t, 0)
	listing := probes
	w.after(t, time.Second)
	assert.Greater(t, probes, listing, "a tick that did not list did not read free space either")
}

// A sweep reads more than the bin, so the listing still comes around.
func TestAnUnchangedBinIsStillListedInTheEnd(t *testing.T) {
	w := watchABin(t)

	w.after(t, 0)
	w.after(t, relistInterval)
	assert.Equal(t, 2, w.b.lists)
}

// Build output past its budget goes on a listing. A changed bin gets one, but
// a bin that changes all day must not be listed all day.
func TestAChangedBinIsListedOnceTheIntervalHasPassed(t *testing.T) {
	w := watchABin(t)
	w.after(t, 0)

	w.recycle(t, "new.o")
	w.after(t, time.Second)
	assert.Equal(t, 1, w.b.lists, "a change was listed before the interval passed")

	w.after(t, settleInterval)
	assert.Equal(t, 2, w.b.lists, "a changed bin was never listed")

	w.after(t, settleInterval)
	assert.Equal(t, 2, w.b.lists, "the same change was listed twice")
}

// A change time moves in steps. A second change inside one step leaves it
// where the first one put it, so a new change time proves nothing.
func TestAChangeTimeTooNewToTrustIsListedAgain(t *testing.T) {
	w := watchABin(t)
	w.recycle(t, "just-now.o")

	w.after(t, 0)
	w.after(t, settleInterval)
	assert.Equal(t, 2, w.b.lists, "a listing trusted a change time from the same instant")

	w.after(t, settleInterval)
	assert.Equal(t, 2, w.b.lists)
}

// Pressure is what the daemon exists for. It is not allowed to wait.
func TestAFilesystemGoingShortIsListedOnThatTick(t *testing.T) {
	w := watchABin(t)
	w.b.items = []bin.Item{w.b.recycled("old.bin", 500, 72)}
	w.after(t, 0)

	w.avail = 50
	evicted, _ := w.after(t, time.Second)
	assert.Equal(t, 2, w.b.lists, "pressure did not get a listing")
	require.Len(t, evicted, 1, "pressure did not evict")
	assert.Equal(t, "old.bin", evicted[0].Item.Name)
}

// The probe in this test never moves, as on a filesystem that live files
// fill. A listing that evicted may have more to give. A listing that had
// nothing to give has nothing more a second later.
func TestAShortFilesystemIsListedAgainOnlyWhileListingHelps(t *testing.T) {
	w := watchABin(t)
	w.b.items = []bin.Item{w.b.recycled("old.bin", 10, 72)}
	w.avail = 50

	evicted, _ := w.after(t, 0)
	require.Len(t, evicted, 1)

	w.after(t, time.Second)
	assert.Equal(t, 2, w.b.lists, "an eviction was not followed up")

	_, pressures := w.after(t, time.Second)
	assert.Equal(t, 2, w.b.lists, "a bin with nothing to give was listed again")
	require.Len(t, pressures, 1, "a short filesystem went unreported on a tick that did not list")
	assert.Equal(t, uint64(50), pressures[0].Avail)
	assert.Equal(t, FreeTarget(2000), pressures[0].Target)
}

// An eviction that failed is tried again for as long as the filesystem is
// short, at the interval and not on every tick.
func TestAStrandedItemIsTriedAgainAtTheInterval(t *testing.T) {
	w := watchABin(t)
	stuck := w.b.recycled("stuck.bin", 300, 72)
	w.b.items = []bin.Item{stuck}
	w.b.fail = map[string]error{stuck.ID: errors.New("permission denied")}
	w.avail = 50

	_, pressures := w.after(t, 0)
	require.Len(t, pressures, 1)
	require.Equal(t, uint64(300), pressures[0].Stranded)

	_, pressures = w.after(t, time.Second)
	assert.Equal(t, 1, w.b.lists, "a failed eviction was retried on the next tick")
	require.Len(t, pressures, 1)
	assert.Equal(t, uint64(300), pressures[0].Stranded, "the report lost what would not go")

	evicted, _ := w.after(t, settleInterval)
	assert.Equal(t, 2, w.b.lists, "a failed eviction was never retried")
	require.Len(t, evicted, 1)
	assert.Error(t, evicted[0].Error)
}

// Something recycled onto a short filesystem is something to give back now.
func TestAChangeInAShortBinIsListedOnThatTick(t *testing.T) {
	w := watchABin(t)
	w.avail = 50
	w.after(t, 0)
	w.after(t, time.Second)
	require.Equal(t, 1, w.b.lists)

	w.recycle(t, "new.o")
	w.after(t, time.Second)
	assert.Equal(t, 2, w.b.lists)
}

// A listing that failed told the watch nothing, so nothing is remembered.
func TestAFailedListingIsTriedAgainOnTheNextTick(t *testing.T) {
	w := watchABin(t)
	w.b.listErr = errors.New("input/output error")

	_, _, err := w.tick()
	require.Error(t, err)

	w.b.listErr = nil
	w.after(t, time.Second)
	assert.Equal(t, 2, w.b.lists)
}
