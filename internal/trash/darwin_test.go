//go:build darwin

package trash

// The macOS backend, from the trash side inwards.
//
// Nothing here recycles anything. So these plant items in the isolated trash
// directly, which is the same state a recycle leaves behind, and drive listing
// and restoring over it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/putback"
)

// plant puts a file in the isolated trash and records where it came from,
// answering the path it now sits at.
func plant(t *testing.T, name, origin, content string) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".Trash")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	file := writeFile(t, filepath.Join(dir, name), content)
	if origin != "" {
		require.NoError(t, putback.Set(dir, name, putback.Of("/", origin)))
	}
	return file
}

func TestAPlantedItemIsListedWithWhereItCameFrom(t *testing.T) {
	work := isolateTrash(t)
	origin := filepath.Join(work, "notes.txt")
	plant(t, "notes.txt", origin, "hello")

	items := mustList(t)
	require.Len(t, items, 1)
	assert.Equal(t, "notes.txt", items[0].Name)
	assert.Equal(t, origin, items[0].OriginalPath)
	assert.EqualValues(t, 5, items[0].Size)
	assert.False(t, items[0].IsDir)
	assert.False(t, items[0].DeletedAt.IsZero())
}

func TestRestoringPutsAnItemBackWhereItCameFrom(t *testing.T) {
	work := isolateTrash(t)
	origin := filepath.Join(work, "docs", "notes.txt")
	plant(t, "notes.txt", origin, "hello")

	items := mustList(t)
	require.Len(t, items, 1)
	dest, err := restore(items[0].ID)
	require.NoError(t, err)
	assert.Equal(t, origin, dest)

	content, err := os.ReadFile(origin)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
	assert.Empty(t, mustList(t), "the item is out of the trash")
}

func TestRestoringClearsThePutBackRecord(t *testing.T) {
	work := isolateTrash(t)
	plant(t, "notes.txt", filepath.Join(work, "notes.txt"), "hello")

	items := mustList(t)
	require.Len(t, items, 1)
	_, err := restore(items[0].ID)
	require.NoError(t, err)

	dir := filepath.Join(os.Getenv("HOME"), ".Trash")
	assert.NotContains(t, putback.Read(dir), "notes.txt")
}

// An item another tool trashed carries no record of where it came from, and
// the caller is told that rather than guessed at.
func TestAnItemWithNoRecordNeedsAnExplicitDestination(t *testing.T) {
	isolateTrash(t)
	file := plant(t, "orphan.txt", "", "hello")

	items := mustList(t)
	require.Len(t, items, 1)
	assert.Empty(t, items[0].OriginalPath)

	_, err := restore(file)
	assert.ErrorIs(t, err, bin.ErrUnknownOrigin)
}

func TestRestoringToAnExplicitDestination(t *testing.T) {
	work := isolateTrash(t)
	file := plant(t, "orphan.txt", "", "hello")

	b, err := Backend()
	require.NoError(t, err)
	dest := filepath.Join(work, "elsewhere", "orphan.txt")
	landed, err := b.Restore(file, dest)
	require.NoError(t, err)
	assert.Equal(t, dest, landed)
	assert.FileExists(t, dest)
}

func TestRestoringNeverOverwrites(t *testing.T) {
	work := isolateTrash(t)
	origin := writeFile(t, filepath.Join(work, "notes.txt"), "the newer one")
	plant(t, "notes.txt", origin, "the trashed one")

	items := mustList(t)
	require.Len(t, items, 1)
	_, err := restore(items[0].ID)
	assert.ErrorIs(t, err, bin.ErrExists)

	content, err := os.ReadFile(origin)
	require.NoError(t, err)
	assert.Equal(t, "the newer one", string(content))
}

// An ID is a path, so it is checked against this user's trash directories
// before anything is moved.
func TestUnknownIDsAreRejectedOnDarwin(t *testing.T) {
	work := isolateTrash(t)
	outside := writeFile(t, filepath.Join(work, "payload.txt"), "hello")
	trash := filepath.Join(os.Getenv("HOME"), ".Trash")

	for _, id := range []string{
		outside,
		filepath.Join(trash, "nothing-here.txt"),
		filepath.Join(trash, "..", "payload.txt"),
		filepath.Join(trash, ".DS_Store"),
	} {
		_, err := restore(id)
		assert.ErrorIs(t, err, bin.ErrNotFound, "id %s", id)
	}
}

// The .DS_Store holding the put back records is trash bookkeeping, not an item.
func TestTheRecordFileIsNotListedAsAnItem(t *testing.T) {
	work := isolateTrash(t)
	plant(t, "notes.txt", filepath.Join(work, "notes.txt"), "hello")

	items := mustList(t)
	require.Len(t, items, 1)
	assert.Equal(t, "notes.txt", items[0].Name)
}

func TestADirectoryIsListedAsOne(t *testing.T) {
	work := isolateTrash(t)
	dir := filepath.Join(os.Getenv("HOME"), ".Trash", "project")
	writeFile(t, filepath.Join(dir, "a.txt"), "hello")
	require.NoError(t, putback.Set(filepath.Dir(dir), "project", putback.Of("/", filepath.Join(work, "project"))))

	items := mustList(t)
	require.Len(t, items, 1)
	assert.True(t, items[0].IsDir)
	assert.EqualValues(t, 5, items[0].Size)
}

// The daemon lists the bin every tick, so an entry's tree is walked once. A
// file written deep inside a trashed directory leaves the entry itself as it
// was. A listing that walked again would see it and one that did not would
// not.
func TestATrashedTreeIsWalkedOncePerEntry(t *testing.T) {
	work := isolateTrash(t)
	dir := filepath.Join(os.Getenv("HOME"), ".Trash", "project")
	writeFile(t, filepath.Join(dir, "src", "a.txt"), "hello")
	require.NoError(t, putback.Set(filepath.Dir(dir), "project", putback.Of("/", filepath.Join(work, "project"))))

	first := mustList(t)
	require.Len(t, first, 1)
	assert.EqualValues(t, 5, first[0].Size)

	writeFile(t, filepath.Join(dir, "src", "b.txt"), "world!")
	again := mustList(t)
	require.Len(t, again, 1)
	assert.EqualValues(t, 5, again[0].Size, "the listing walked the tree again")

	// An entry the last listing never saw is measured.
	plant(t, "notes.txt", filepath.Join(work, "notes.txt"), "abc")
	var sizes []int64
	for _, it := range mustList(t) {
		sizes = append(sizes, it.Size)
	}
	assert.ElementsMatch(t, []int64{5, 3}, sizes)
}

// The home trash is the volume root's own, so a location recorded in it is
// read back against "/".
func TestTheHomeTrashRecordsLocationsAgainstTheVolumeRoot(t *testing.T) {
	isolateTrash(t)
	b, err := Backend()
	require.NoError(t, err)
	mac := b.(*macTrash)
	assert.Equal(t, "/", mac.volumeRoot(mac.home))
	assert.Equal(t, "/Volumes/disk", mac.volumeRoot("/Volumes/disk/.Trashes/501"))
	assert.Contains(t, mac.Dirs(), mac.home)
}

// Finder writes a location through the data volume's own mount point. That
// path and the firmlinked a single name the same file, and the short a single
// is what a person types.
func TestADataVolumePathIsPresentedAsTheFirmlinkedOne(t *testing.T) {
	assert.Equal(t, "/Users", dataVolumePath("/System/Volumes/Data/Users"))
	assert.Equal(t, "/etc/hosts", dataVolumePath("/etc/hosts"))
	assert.Equal(t,
		"/System/Volumes/Data/nowhere/at/all/file.txt",
		dataVolumePath("/System/Volumes/Data/nowhere/at/all/file.txt"),
		"a location whose directory does not exist keeps what was recorded")
}
