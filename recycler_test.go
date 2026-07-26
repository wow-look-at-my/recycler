package recycler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolateTrash points the recycle bin at a temporary directory so tests never
// touch the developer's real one, and returns a scratch directory to recycle
// files from. Both live on the same filesystem, which is what makes recycling a
// rename.
func isolateTrash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the Windows recycle bin is a system location and cannot be redirected for tests")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	return work
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func mustList(t *testing.T) []Item {
	t.Helper()
	items, err := List()
	require.NoError(t, err)
	return items
}

func TestRecycleAndRestore(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "notes.txt"), "keep me")

	require.NoError(t, Recycle(path))
	assert.NoFileExists(t, path, "the recycled file is still at its original location")

	items := mustList(t)
	require.Len(t, items, 1)
	item := items[0]
	assert.Equal(t, "notes.txt", item.Name)
	assert.Equal(t, path, item.OriginalPath)
	assert.Equal(t, int64(len("keep me")), item.Size)
	assert.False(t, item.IsDir)
	assert.False(t, item.DeletedAt.IsZero(), "DeletedAt was not recorded")

	restored, err := Restore(item.ID)
	require.NoError(t, err)
	assert.Equal(t, path, restored)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "keep me", string(content))
	assert.Empty(t, mustList(t), "the recycle bin should be empty after restoring")
}

func TestRecycleDirectory(t *testing.T) {
	work := isolateTrash(t)
	dir := filepath.Join(work, "project")
	writeFile(t, filepath.Join(dir, "a.txt"), "aaa")
	writeFile(t, filepath.Join(dir, "sub", "b.txt"), "bbbb")

	require.NoError(t, Recycle(dir))

	items := mustList(t)
	require.Len(t, items, 1)
	assert.True(t, items[0].IsDir, "a recycled directory should report IsDir")
	assert.Equal(t, int64(len("aaa")+len("bbbb")), items[0].Size, "Size should be the total size of the contents")

	_, err := Restore(items[0].ID)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "sub", "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "bbbb", string(content))
}

func TestRecycleKeepsNamesApart(t *testing.T) {
	work := isolateTrash(t)
	first := writeFile(t, filepath.Join(work, "one", "same.txt"), "first")
	second := writeFile(t, filepath.Join(work, "two", "same.txt"), "second")

	require.NoError(t, Recycle(first, second))

	items := mustList(t)
	require.Len(t, items, 2)
	require.NotEqual(t, items[0].ID, items[1].ID, "two files with the same name share an ID")

	// Each one has to restore to its own original location, with its own
	// content.
	for _, item := range items {
		_, err := Restore(item.ID)
		require.NoError(t, err)
	}
	for path, want := range map[string]string{first: "first", second: "second"} {
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, want, string(content))
	}
}

func TestRestoreToExplicitDestination(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "move-me.txt"), "hello")
	require.NoError(t, Recycle(path))

	items := mustList(t)
	require.Len(t, items, 1)

	dest := filepath.Join(work, "elsewhere", "renamed.txt")
	restored, err := RestoreTo(items[0].ID, dest)
	require.NoError(t, err)
	assert.Equal(t, dest, restored)

	content, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

func TestRestoreRefusesToOverwrite(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "busy.txt"), "recycled")
	require.NoError(t, Recycle(path))
	writeFile(t, path, "new file in the old place")

	items := mustList(t)
	require.Len(t, items, 1)

	_, err := Restore(items[0].ID)
	require.ErrorIs(t, err, ErrExists)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new file in the old place", string(content), "the existing file was disturbed")
	assert.Len(t, mustList(t), 1, "the item should still be in the recycle bin after a refused restore")
}

func TestPurgeAndEmpty(t *testing.T) {
	work := isolateTrash(t)
	first := writeFile(t, filepath.Join(work, "a.txt"), "a")
	second := writeFile(t, filepath.Join(work, "b.txt"), "b")
	third := writeFile(t, filepath.Join(work, "c.txt"), "c")
	require.NoError(t, Recycle(first, second, third))

	items := mustList(t)
	require.Len(t, items, 3)

	require.NoError(t, Purge(items[0].ID))
	assert.NoFileExists(t, items[0].ID, "the purged file is still on disk")
	assert.Len(t, mustList(t), 2)

	require.NoError(t, Empty())
	assert.Empty(t, mustList(t), "the recycle bin is not empty")
}

func TestUnknownIDsAreRejected(t *testing.T) {
	work := isolateTrash(t)
	outsider := writeFile(t, filepath.Join(work, "innocent.txt"), "do not touch")

	// Recycle something so the trash directories exist.
	require.NoError(t, Recycle(writeFile(t, filepath.Join(work, "decoy.txt"), "decoy")))

	for _, id := range []string{"", "not-an-id", outsider, filepath.Join(work, "nope", "files", "x")} {
		_, getErr := Get(id)
		assert.ErrorIs(t, getErr, ErrNotFound, "Get(%q)", id)

		_, restoreErr := RestoreTo(id, filepath.Join(work, "out"))
		assert.ErrorIs(t, restoreErr, ErrNotFound, "RestoreTo(%q)", id)

		assert.ErrorIs(t, Purge(id), ErrNotFound, "Purge(%q)", id)
	}

	// Nothing outside the recycle bin may be touched by a bad ID.
	content, err := os.ReadFile(outsider)
	require.NoError(t, err)
	assert.Equal(t, "do not touch", string(content), "a rejected ID disturbed a file outside the recycle bin")
	assert.Len(t, mustList(t), 1, "rejected IDs changed the contents of the recycle bin")
}

func TestRecycleReportsMissingPaths(t *testing.T) {
	work := isolateTrash(t)
	good := writeFile(t, filepath.Join(work, "good.txt"), "good")

	err := Recycle(filepath.Join(work, "absent.txt"), good)
	require.Error(t, err, "expected an error for the missing path")
	assert.Contains(t, err.Error(), "absent.txt", "the error does not mention the failing path")

	// The path that does exist still has to be recycled.
	items := mustList(t)
	require.Len(t, items, 1, "the valid path was not recycled")
	assert.Equal(t, "good.txt", items[0].Name)
}

func TestRecycleNothing(t *testing.T) {
	isolateTrash(t)
	assert.NoError(t, Recycle())
	assert.NoError(t, Purge())
}

func TestListOnAnEmptyBin(t *testing.T) {
	isolateTrash(t)
	items, err := List()
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestItemString(t *testing.T) {
	when := time.Date(2026, 7, 26, 11, 24, 9, 0, time.UTC)
	described := Item{Name: "notes.txt", OriginalPath: "/home/user/notes.txt", DeletedAt: when}
	assert.Equal(t, "/home/user/notes.txt [2026-07-26T11:24:09Z]", described.String())

	unknown := Item{Name: "notes.txt", DeletedAt: when}
	assert.Contains(t, unknown.String(), "notes.txt")
	assert.Contains(t, unknown.String(), "unknown")
}

func TestAvailable(t *testing.T) {
	assert.True(t, Available(), "no recycle bin implementation for %s", runtime.GOOS)
}
