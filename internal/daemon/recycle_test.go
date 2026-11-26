package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

// A shelfBin is a bin in a directory of the test's own. It moves what it is
// handed, and never deletes a path it is asked to recycle.
type shelfBin struct {
	dir     string
	items   []bin.Item
	evicted []string
	asked   []string
	fail    map[string]error
}

func (b *shelfBin) Recycle(paths []string) ([]bin.Disposal, error) {
	var disposals []bin.Disposal
	var errs []error
	for _, path := range paths {
		b.asked = append(b.asked, path)
		dest := filepath.Join(b.dir, filepath.Base(path))
		if err := os.Rename(path, dest); err != nil {
			errs = append(errs, err)
			continue
		}
		b.items = append(b.items, bin.Item{ID: dest, Name: filepath.Base(path), OriginalPath: path, DeletedAt: time.Now()})
		disposals = append(disposals, bin.Disposal{Path: path})
	}
	return disposals, errors.Join(errs...)
}

func (b *shelfBin) List() ([]bin.Item, error)              { return b.items, nil }
func (b *shelfBin) Restore(string, string) (string, error) { return "", nil }
func (b *shelfBin) Dirs() []string                         { return []string{b.dir} }
func (b *shelfBin) DirFor(string) (string, error)          { return b.dir, nil }

func (b *shelfBin) Evict(id string) error {
	if err, bad := b.fail[id]; bad {
		return err
	}
	b.evicted = append(b.evicted, filepath.Base(id))
	kept := b.items[:0]
	for _, it := range b.items {
		if it.ID != id {
			kept = append(kept, it)
		}
	}
	b.items = kept
	return os.RemoveAll(id)
}

// hold puts an item of a stated size and age in the bin.
func (b *shelfBin) hold(t *testing.T, name string, size int64, ageHours int) string {
	t.Helper()
	id := filepath.Join(b.dir, name)
	require.NoError(t, os.WriteFile(id, []byte(name), 0o600))
	b.items = append(b.items, bin.Item{ID: id, Name: name, Size: size, DeletedAt: time.Now().Add(-time.Duration(ageHours) * time.Hour)})
	return id
}

func (b *shelfBin) names() []string {
	names := make([]string, 0, len(b.items))
	for _, it := range b.items {
		names = append(names, it.Name)
	}
	return names
}

// A shelf is a work directory and a bin, each on a filesystem the test states.
type shelf struct {
	work string
	bin  *shelfBin
	free map[string][2]uint64
}

func newShelf(t *testing.T) *shelf {
	t.Helper()
	root := t.TempDir()
	s := &shelf{work: filepath.Join(root, "work"), bin: &shelfBin{dir: filepath.Join(root, "bin")}, free: map[string][2]uint64{}}
	require.NoError(t, os.Mkdir(s.work, 0o700))
	require.NoError(t, os.Mkdir(s.bin.dir, 0o700))
	return s
}

// disk names the stated filesystem holding path: the longest directory a test gave numbers for.
func (s *shelf) disk(path string) string {
	best := ""
	for dir := range s.free {
		if (path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))) && len(dir) > len(best) {
			best = dir
		}
	}
	return best
}

func (s *shelf) probe(dir string) (uint64, uint64, error) {
	n, ok := s.free[s.disk(dir)]
	if !ok {
		return 0, 0, errors.New("no such filesystem")
	}
	return n[0], n[1], nil
}

func (s *shelf) same(a, b string) (bool, error) { return s.disk(a) == s.disk(b), nil }

func (s *shelf) recycle(paths ...string) ([]bin.Disposal, error) {
	return recycleInto(s.bin, paths, s.probe, s.same)
}

func (s *shelf) file(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(s.work, name)
	require.NoError(t, os.WriteFile(path, make([]byte, 16<<10), 0o600))
	return path
}

const smallDisk = 40_000

func TestARecycleEvictsTheOldestItemsToMakeRoom(t *testing.T) {
	s := newShelf(t)
	s.free[filepath.Dir(s.work)] = [2]uint64{400, smallDisk}
	s.bin.hold(t, "mid", 300, 48)
	s.bin.hold(t, "old", 500, 72)
	s.bin.hold(t, "new", 900, 1)
	path := s.file(t, "doomed.txt")

	disposals, err := s.recycle(path)
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.False(t, disposals[0].Permanent, "the new path was deleted: %s", disposals[0].Reason)
	assert.Equal(t, []string{"old", "mid"}, s.bin.evicted, "the oldest go first, and no more than the floor needs")
	require.Len(t, disposals[0].Evicted, 2, "what the bin gave back is reported")
	assert.Contains(t, disposals[0].Reason, "under the")

	assert.Equal(t, []string{"new", "doomed.txt"}, s.bin.names())
	assert.FileExists(t, filepath.Join(s.bin.dir, "doomed.txt"))
}

func TestARecycleWithRoomEvictsNothing(t *testing.T) {
	s := newShelf(t)
	s.free[filepath.Dir(s.work)] = [2]uint64{1000, smallDisk}
	s.bin.hold(t, "old", 500, 72)

	disposals, err := s.recycle(s.file(t, "kept.txt"))
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.False(t, disposals[0].Permanent)
	assert.Empty(t, disposals[0].Evicted)
	assert.Empty(t, disposals[0].Reason)
	assert.Empty(t, s.bin.evicted)
}

// A path is deleted only when all the bin holds there cannot make the room.
// The bin keeps what it has then: its loss as well gains nothing.
func TestAPathIsDeletedOnlyWhenTheBinCannotMakeRoom(t *testing.T) {
	s := newShelf(t)
	s.free[filepath.Dir(s.work)] = [2]uint64{400, smallDisk}
	s.bin.hold(t, "crumb", 100, 72)
	s.bin.hold(t, "unsized", bin.SizeUnknown, 96)
	path := s.file(t, "doomed.txt")

	disposals, err := s.recycle(path)
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.True(t, disposals[0].Permanent)
	assert.Contains(t, disposals[0].Reason, "too little")
	assert.Empty(t, disposals[0].Evicted)
	assert.NoFileExists(t, path)
	assert.Empty(t, s.bin.evicted, "the bin lost items that made no room")
	assert.Empty(t, s.bin.asked, "a deleted path still went to the backend")
}

// A rename into a bin on the same filesystem needs no free space, so no size
// turns it into a deletion or an eviction.
func TestAMoveWithinOneFilesystemIsNeverDeletedForItsSize(t *testing.T) {
	s := newShelf(t)
	s.free[filepath.Dir(s.work)] = [2]uint64{1000, smallDisk}
	s.bin.hold(t, "old", 500, 72)
	path := s.file(t, "large.bin")
	require.Greater(t, fsutil.DiskUsage(path), int64(1000), "the fixture fits in the free space")

	disposals, err := s.recycle(path)
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.False(t, disposals[0].Permanent, "a same-filesystem move was turned into a deletion: %s", disposals[0].Reason)
	assert.Empty(t, s.bin.evicted)
}

// A bin on another filesystem takes a copy, which needs room there for the
// blocks the path occupies.
func TestACopyThatDoesNotFitEvictsOnTheFilesystemOfTheBin(t *testing.T) {
	s := newShelf(t)
	path := s.file(t, "large.bin")
	size := uint64(fsutil.DiskUsage(path))
	s.free[s.work] = [2]uint64{0, smallDisk}
	s.free[s.bin.dir] = [2]uint64{size - 10, 400 * size}
	s.bin.hold(t, "old", 10, 72)
	s.bin.hold(t, "new", 10, 1)

	disposals, err := s.recycle(path)
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.False(t, disposals[0].Permanent, "a full source filesystem deleted a path a move off it frees: %s", disposals[0].Reason)
	assert.Equal(t, []string{"old"}, s.bin.evicted)
	assert.Contains(t, disposals[0].Reason, "does not fit")
}

func TestACopyTheBinCannotMakeRoomForIsDeleted(t *testing.T) {
	s := newShelf(t)
	path := s.file(t, "large.bin")
	size := uint64(fsutil.DiskUsage(path))
	s.free[s.work] = [2]uint64{size, 40 * size}
	s.free[s.bin.dir] = [2]uint64{size - 10, 400 * size}
	s.bin.hold(t, "old", 5, 72)

	disposals, err := s.recycle(path)
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.True(t, disposals[0].Permanent)
	assert.Contains(t, disposals[0].Reason, "does not fit")
	assert.Empty(t, s.bin.evicted)
}

// One batch accounts for what it freed. A second read of a filesystem that
// reports freed blocks late evicts for the same bytes twice.
func TestABatchEvictsOnceForOneShortfall(t *testing.T) {
	s := newShelf(t)
	s.free[filepath.Dir(s.work)] = [2]uint64{400, smallDisk}
	s.bin.hold(t, "old", 700, 72)
	s.bin.hold(t, "mid", 700, 48)
	a, b := s.file(t, "a.txt"), s.file(t, "b.txt")

	disposals, err := s.recycle(a, b)
	require.NoError(t, err)
	require.Len(t, disposals, 2)
	assert.Equal(t, a, disposals[0].Path)
	assert.Equal(t, b, disposals[1].Path)
	assert.Len(t, disposals[0].Evicted, 1)
	assert.Empty(t, disposals[1].Evicted)
	assert.Equal(t, []string{"old"}, s.bin.evicted)
}

// An eviction that failed made no room. The next oldest item takes its place.
func TestAFailedEvictionMakesNoRoom(t *testing.T) {
	s := newShelf(t)
	s.free[filepath.Dir(s.work)] = [2]uint64{400, smallDisk}
	stuck := s.bin.hold(t, "stuck", 700, 72)
	s.bin.hold(t, "mid", 700, 48)
	s.bin.fail = map[string]error{stuck: errors.New("permission denied")}

	disposals, err := s.recycle(s.file(t, "a.txt"))
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.False(t, disposals[0].Permanent)
	assert.Equal(t, []string{"mid"}, s.bin.evicted)

	// Nothing else is left to give, so the next path under the floor is deleted.
	s.free[filepath.Dir(s.work)] = [2]uint64{400, smallDisk}
	disposals, err = s.recycle(s.file(t, "b.txt"))
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.True(t, disposals[0].Permanent)
}

// No measurement is not a licence to evict or to delete.
func TestAnUnreadableFilesystemRecyclesNormally(t *testing.T) {
	s := newShelf(t)
	s.bin.hold(t, "old", 500, 72)

	disposals, err := s.recycle(s.file(t, "kept.txt"))
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.False(t, disposals[0].Permanent)
	assert.Empty(t, s.bin.evicted)
}

// A path the backend refuses is never deleted in its place, a filesystem root above all.
func TestPressureNeverDeletesWhatTheBackendRefuses(t *testing.T) {
	s := newShelf(t)
	s.free[filepath.Dir(s.work)] = [2]uint64{0, smallDisk}
	s.free[string(filepath.Separator)] = [2]uint64{0, smallDisk}
	missing := filepath.Join(s.work, "missing.txt")

	root := filepath.VolumeName(s.work) + string(filepath.Separator)
	_, err := s.recycle(root, missing)
	require.Error(t, err, "the backend reports both")
	assert.Equal(t, []string{root, missing}, s.bin.asked)
	assert.DirExists(t, s.work)
}

// A capped folder recycles its files inside the daemon. What a recycle there
// cost the bin is reported with what the cap gave back. A file that was
// deleted is not reported as recycled.
func TestAFolderCapReportsWhatItsRecyclesCost(t *testing.T) {
	s := newShelf(t)
	s.free[filepath.Dir(s.work)] = [2]uint64{400, smallDisk}
	s.bin.hold(t, "old", 700, 72)
	first, second := s.file(t, "first.tmp"), s.file(t, "second.tmp")
	cost := &recycleCost{b: s.bin, free: s.probe, same: s.same, dir: s.work}

	require.NoError(t, cost.give(bin.Item{ID: first}))
	// Each give reads the filesystem again, and the bin now holds nothing sized.
	require.NoError(t, cost.give(bin.Item{ID: second}))

	evicted := cost.report([]Eviction{
		{Item: bin.Item{ID: first}, Reason: "recycled as the least recently used"},
		{Item: bin.Item{ID: second}, Reason: "recycled as the least recently used"},
	})
	require.Len(t, evicted, 3)
	assert.Equal(t, "recycled as the least recently used", evicted[0].Reason)
	assert.Contains(t, evicted[1].Reason, "deleted, not recycled")
	assert.Equal(t, "old", evicted[2].Item.Name, "the bin item evicted for room went unreported")
	assert.Contains(t, evicted[2].Reason, "made room for a recycle from "+s.work)

	var none *recycleCost
	assert.Len(t, none.report(evicted[:1]), 1, "a cap that deletes has no recycle to report")
}

// The real bin under a stated shortfall: the item goes, the new one is
// recycled.
func TestTheRealBinGivesBackAnOldItemForANewOne(t *testing.T) {
	home := isolateBin(t)
	b, err := trash.Backend()
	require.NoError(t, err)
	old := filepath.Join(home, "old.bin")
	require.NoError(t, os.WriteFile(old, make([]byte, 800), 0o600))
	_, err = recycleInto(b, []string{old}, freeSpace(1000, smallDisk), fsutil.SameFilesystem)
	require.NoError(t, err)

	fresh := filepath.Join(home, "fresh.txt")
	require.NoError(t, os.WriteFile(fresh, []byte("fresh"), 0o600))
	disposals, err := recycleInto(b, []string{fresh}, freeSpace(400, smallDisk), fsutil.SameFilesystem)
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.False(t, disposals[0].Permanent, "the new path was deleted: %s", disposals[0].Reason)
	require.Len(t, disposals[0].Evicted, 1)
	assert.Equal(t, old, disposals[0].Evicted[0].OriginalPath)

	items, err := b.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, fresh, items[0].OriginalPath)
}
