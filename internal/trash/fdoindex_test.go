//go:build linux

package trash

import (
	"os"
	"path/filepath"
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
	changed, err = index.Refresh()
	require.NoError(t, err)
	assert.Equal(t, []string{homeTrash(t)}, changed)
	want, err := fdoList()
	require.NoError(t, err)
	assert.Equal(t, want, index.List())

	require.NoError(t, testFdo().Evict(want[0].ID))
	_, err = index.Refresh()
	require.NoError(t, err)
	assert.Len(t, index.List(), 1, "an evicted item stays in the index")
}

// A files/ or info/ removed and made again must not leave the index blind.
func TestTheIndexSurvivesABinMadeAgain(t *testing.T) {
	work := isolateTrash(t)
	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "one.txt"), "bytes")))
	index := watchTheBin(t)
	require.Len(t, index.List(), 1)

	require.NoError(t, os.RemoveAll(homeTrash(t)))
	_, err := index.Refresh()
	require.NoError(t, err)
	assert.Empty(t, index.List())

	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "two.txt"), "bytes")))
	_, err = index.Refresh()
	require.NoError(t, err)
	assert.Equal(t, []string{"two.txt"}, names(index.List()))
}

// An unrecorded entry the watch sees gets a record, as a listing gives it one.
func TestTheIndexAdoptsAnUnrecordedEntry(t *testing.T) {
	isolateTrash(t)
	require.NoError(t, ensureTrashDir(homeTrash(t)))
	index := watchTheBin(t)

	writeFile(t, filepath.Join(homeTrash(t), trashFilesDir, "stray"), "abc")
	_, err := index.Refresh()
	require.NoError(t, err)
	require.Equal(t, []string{"stray"}, names(index.List()))
	assert.FileExists(t, filepath.Join(homeTrash(t), trashInfoDir, "stray"+trashInfoExt))
}

// The directorysizes cache beside files/ and info/ changes on every recycle
// another implementation makes, and must not read the bin again.
func TestTheDirectorySizesCacheDoesNotReadTheBin(t *testing.T) {
	work := isolateTrash(t)
	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "one.txt"), "bytes")))
	index := watchTheBin(t)

	writeFile(t, filepath.Join(homeTrash(t), "directorysizes"), "1 2 x\n")
	changed, err := index.Refresh()
	require.NoError(t, err)
	assert.Empty(t, changed)
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
