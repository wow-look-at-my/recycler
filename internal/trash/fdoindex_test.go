//go:build linux

package trash

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

func watchTheBin(t *testing.T) *fdoIndex {
	t.Helper()
	index, err := testFdo().Watch()
	require.NoError(t, err)
	t.Cleanup(func() { index.Close() })
	return index.(*fdoIndex)
}

// refreshUntil refreshes until done holds, and answers every directory the
// refreshes reported. Some file systems deliver an event a few milliseconds
// after the change that caused it.
func refreshUntil(t *testing.T, index *fdoIndex, done func() bool) []string {
	t.Helper()
	var changed []string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		these, err := index.Refresh()
		require.NoError(t, err)
		changed = append(changed, these...)
		if done() {
			return changed
		}
	}
	t.Fatalf("the index never caught up: it lists %v", names(index.List()))
	return nil
}

func names(items []bin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, filepath.Base(it.ID))
	}
	return out
}

// The index must list what a listing lists, and follow each change from the
// watch alone.
func TestTheIndexFollowsTheBin(t *testing.T) {
	work := isolateTrash(t)
	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "before.txt"), "bytes")))
	index := watchTheBin(t)
	assert.Equal(t, []string{"before.txt"}, names(index.List()))

	changed, err := index.Refresh()
	require.NoError(t, err)
	assert.Empty(t, changed, "nothing changed, so nothing may be read again")

	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "after.txt"), "more bytes")))
	changed = refreshUntil(t, index, func() bool { return len(index.List()) == 2 })
	assert.Contains(t, changed, homeTrash(t))
	want, err := fdoList()
	require.NoError(t, err)
	assert.Equal(t, want, index.List())
	assert.Equal(t, 1, index.reads, "a recycle read the whole bin again")

	require.NoError(t, testFdo().Evict(want[0].ID))
	refreshUntil(t, index, func() bool { return len(index.List()) == 1 })
}

// A files/ or info/ removed and made again must not leave the index blind.
func TestTheIndexSurvivesABinMadeAgain(t *testing.T) {
	work := isolateTrash(t)
	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "one.txt"), "bytes")))
	index := watchTheBin(t)
	require.Len(t, index.List(), 1)

	require.NoError(t, os.RemoveAll(homeTrash(t)))
	refreshUntil(t, index, func() bool { return len(index.List()) == 0 })

	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "two.txt"), "bytes")))
	refreshUntil(t, index, func() bool { return slices.Equal(names(index.List()), []string{"two.txt"}) })
}

// An unrecorded entry the watch sees gets a record, as a listing gives it one.
func TestTheIndexAdoptsAnUnrecordedEntry(t *testing.T) {
	isolateTrash(t)
	require.NoError(t, ensureTrashDir(homeTrash(t)))
	index := watchTheBin(t)

	writeFile(t, filepath.Join(homeTrash(t), trashFilesDir, "stray"), "abc")
	refreshUntil(t, index, func() bool { return slices.Equal(names(index.List()), []string{"stray"}) })
	assert.FileExists(t, filepath.Join(homeTrash(t), trashInfoDir, "stray"+trashInfoExt))
}

// The directorysizes cache beside files/ and info/ changes on every recycle
// another implementation makes, and must not read the bin again.
func TestTheDirectorySizesCacheDoesNotReadTheBin(t *testing.T) {
	work := isolateTrash(t)
	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "one.txt"), "bytes")))
	index := watchTheBin(t)

	writeFile(t, filepath.Join(homeTrash(t), "directorysizes"), "1 2 x\n")
	// A recycle after it, so the refresh below has seen both.
	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "two.txt"), "bytes")))
	refreshUntil(t, index, func() bool { return len(index.List()) == 2 })
	assert.Equal(t, 1, index.reads, "the directorysizes cache read the whole bin again")
}

// A record whose file has been gone for long is removed, so no listing reads it
// again. A young one is a recycle or a restore in progress, and stays.
func TestAnOrphanRecordIsRemovedOnceItIsOld(t *testing.T) {
	isolateTrash(t)
	trash := homeTrash(t)
	require.NoError(t, ensureTrashDir(trash))
	old := writeFile(t, filepath.Join(trash, trashInfoDir, "old.staging"+trashInfoExt), "[Trash Info]\nPath=/x\n")
	young := writeFile(t, filepath.Join(trash, trashInfoDir, "young"+trashInfoExt), "[Trash Info]\nPath=/y\n")
	when := time.Now().Add(-2 * orphanGrace)
	require.NoError(t, os.Chtimes(old, when, when))

	items, err := fdoList()
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.NoFileExists(t, old, "the orphan record is read again by every listing")
	assert.FileExists(t, young, "a record written moments ago belongs to a recycle in progress")
}
