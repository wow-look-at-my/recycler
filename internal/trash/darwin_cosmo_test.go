//go:build cosmo

package trash

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/putback"
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

	// Every volume of the host keeps a trash of its own, which HOME does not move.
	mounts := Mounts
	Mounts = func() []string { return nil }
	t.Cleanup(func() { Mounts = mounts })

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

// A trim hands over thousands of paths at once. Each put back record costs a
// rewrite of the whole .DS_Store, so the call must pay for one, not one per path.
func TestTheMacBinRecordsABatchInOneRewrite(t *testing.T) {
	b, work := isolateMac(t)
	var paths []string
	for dir := range 3 {
		for i := range 200 {
			// The same names under each directory.
			paths = append(paths, writeMacFile(t, filepath.Join(work, fmt.Sprintf("crate-%d", dir), fmt.Sprintf("dep-%03d.rlib", i))))
		}
	}

	before := putback.Rewrites.Load()
	disposals, err := b.Recycle(paths)
	require.NoError(t, err)
	assert.Equal(t, int64(1), putback.Rewrites.Load()-before, "the .DS_Store was rewritten more than once for one batch")
	assert.Len(t, disposals, len(paths))

	items, err := b.List()
	require.NoError(t, err)
	recorded := set.New[string](len(items))
	for _, item := range items {
		recorded.Add(item.OriginalPath)
	}
	for _, path := range paths {
		assert.NoFileExists(t, path)
		assert.True(t, recorded.Contains(path), "%s is in the trash with no put back record", path)
	}
}

// A path that cannot move after its record was written gets that record taken
// back, and costs one more rewrite, not one per path.
func TestTheMacBinRetractsTheRecordOfAPathThatDidNotMove(t *testing.T) {
	b, work := isolateMac(t)
	dir := filepath.Join(work, "target")
	inner := writeMacFile(t, filepath.Join(dir, "inner.o"))
	other := writeMacFile(t, filepath.Join(work, "other.o"))

	before := putback.Rewrites.Load()
	disposals, err := b.Recycle([]string{dir, inner, other})
	require.Error(t, err, "a path that had already left with its directory was reported recycled")
	assert.Contains(t, err.Error(), inner)
	assert.Equal(t, int64(2), putback.Rewrites.Load()-before)
	assert.Equal(t, []bin.Disposal{{Path: dir}, {Path: other}}, disposals)

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, map[string]putback.Origin{
		"target":  putback.Of("/", dir),
		"other.o": putback.Of("/", other),
	}, putback.Read(filepath.Join(home, ".Trash")), "the record of the path that never arrived was left behind")
}

func writeMacFile(t *testing.T, path string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("build output"), 0o644))
	return path
}

// An evicted item takes its put back record with it. A record left behind for
// every evicted item makes each later write to the .DS_Store slower.
func TestTheMacBinEvictsTheRecordWithTheItem(t *testing.T) {
	b, work := isolateMac(t)
	path := filepath.Join(work, "doomed.txt")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
	_, err := b.Recycle([]string{path})
	require.NoError(t, err)

	items, err := b.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	dir, name := filepath.Dir(items[0].ID), filepath.Base(items[0].ID)
	require.Contains(t, putback.Read(dir), name)

	require.NoError(t, b.Evict(items[0].ID))
	assert.NoFileExists(t, items[0].ID)
	assert.NotContains(t, putback.Read(dir), name, "the put back record outlived the evicted item")
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
