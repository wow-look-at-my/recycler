package recycler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUniqueName(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "report.txt", uniqueName("report.txt", dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "report.txt"), nil, 0o600))
	assert.Equal(t, "report_1.txt", uniqueName("report.txt", dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "report_1.txt"), nil, 0o600))
	assert.Equal(t, "report_2.txt", uniqueName("report.txt", dir))

	assert.Equal(t, "recycled", uniqueName("..", dir), "a name that is not a file name should be replaced")
}

func TestMoveRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	for _, path := range []string{src, dst} {
		require.NoError(t, os.WriteFile(path, []byte(path), 0o600))
	}

	require.ErrorIs(t, move(src, dst), ErrExists)

	content, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, dst, string(content), "the destination was modified")
}

func TestCopyTree(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "sub", "file.txt"), []byte("content"), 0o600))
	require.NoError(t, os.Symlink("file.txt", filepath.Join(src, "sub", "link")))

	dst := filepath.Join(dir, "copy")
	require.NoError(t, copyTree(src, dst))

	content, err := os.ReadFile(filepath.Join(dst, "sub", "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content", string(content))

	target, err := os.Readlink(filepath.Join(dst, "sub", "link"))
	require.NoError(t, err)
	assert.Equal(t, "file.txt", target, "the symlink was not copied as a symlink")
}

func TestTreeSize(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a"), []byte("12345"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b"), []byte("123"), 0o600))

	assert.Equal(t, int64(8), treeSize(dir), "a directory should report the total size of its contents")
	assert.Equal(t, int64(5), treeSize(filepath.Join(dir, "a")))
	assert.Equal(t, SizeUnknown, treeSize(filepath.Join(dir, "missing")))
}

func TestSortItemsNewestFirst(t *testing.T) {
	now := time.Now()
	items := []Item{
		{ID: "old", DeletedAt: now.Add(-time.Hour)},
		{ID: "new", DeletedAt: now},
		{ID: "b", DeletedAt: now.Add(-time.Minute)},
		{ID: "a", DeletedAt: now.Add(-time.Minute)},
	}
	sortItems(items)
	assert.Equal(t, []string{"new", "a", "b", "old"}, ids(items))
}

func TestIsCrossDevice(t *testing.T) {
	assert.True(t, isCrossDevice(&os.LinkError{Op: "rename", Err: errCrossDevice}))
	assert.False(t, isCrossDevice(&os.LinkError{Op: "rename", Err: os.ErrPermission}))
	assert.False(t, isCrossDevice(os.ErrNotExist))
	assert.False(t, isCrossDevice(nil))
}

func TestPrepareDest(t *testing.T) {
	dir := t.TempDir()

	dest := filepath.Join(dir, "a", "b", "file.txt")
	require.NoError(t, prepareDest(dest))
	assert.DirExists(t, filepath.Dir(dest), "the parent directories should have been created")

	require.NoError(t, os.WriteFile(dest, nil, 0o600))
	assert.ErrorIs(t, prepareDest(dest), ErrExists)
}

func ids(items []Item) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.ID
	}
	return out
}
