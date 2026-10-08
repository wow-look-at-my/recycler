//go:build cosmo

package trash

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// A cosmo build carries the macOS backend, so this exercises it on whatever
// host runs the tests. It reaches only what is portable: the trash layout, the
// put back records, and the ID validation. Finder is not here to read them.

// isolateMac points the macOS bin at a temporary directory and answers a
// working directory to recycle from.
func isolateMac(t *testing.T) (bin.Backend, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	t.Setenv("HOME", home)

	b, err := macBackend()
	require.NoError(t, err)

	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	return b, work
}

// The whole round trip: an item goes to ~/.Trash, lists with where it came
// from, and comes back to that place.
func TestTheMacBinRecyclesListsAndRestores(t *testing.T) {
	b, work := isolateMac(t)
	path := filepath.Join(work, "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0o644))

	_, err := b.Recycle([]string{path})
	require.NoError(t, err)
	assert.NoFileExists(t, path, "the file is still where it was")

	items, err := b.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "notes.txt", items[0].Name)
	assert.Equal(t, path, items[0].OriginalPath, "the put back record names where it came from")

	back, err := b.Restore(items[0].ID, "")
	require.NoError(t, err)
	assert.Equal(t, path, back)
	assert.FileExists(t, path)

	after, err := b.List()
	require.NoError(t, err)
	assert.Empty(t, after, "the record outlived the item")
}

// An ID is a path inside this user's bin. Anything else must not be reached,
// whether to restore it or to destroy it.
func TestTheMacBinRejectsAnIDOutsideIt(t *testing.T) {
	b, work := isolateMac(t)
	outsider := filepath.Join(work, "innocent.txt")
	require.NoError(t, os.WriteFile(outsider, []byte("do not touch"), 0o644))

	for _, id := range []string{"", "not-an-id", outsider, filepath.Join(work, "nope")} {
		_, err := b.Restore(id, "")
		assert.ErrorIs(t, err, bin.ErrNotFound, "restore(%q)", id)
		assert.ErrorIs(t, b.Evict(id), bin.ErrNotFound, "evict(%q)", id)
	}
	assert.FileExists(t, outsider, "a hostile ID reached outside the bin")
}

// The daemon watches the filesystems behind these, so the home bin is named
// whether anything has been recycled into it yet.
func TestTheMacBinNamesItsDirectories(t *testing.T) {
	b, _ := isolateMac(t)
	dirs := b.Dirs()
	require.NotEmpty(t, dirs)
	assert.Contains(t, dirs[0], ".Trash")
}
