package fsutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

func TestUniqueName(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "report.txt", UniqueName("report.txt", dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "report.txt"), nil, 0o600))
	assert.Equal(t, "report_1.txt", UniqueName("report.txt", dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "report_1.txt"), nil, 0o600))
	assert.Equal(t, "report_2.txt", UniqueName("report.txt", dir))

	assert.Equal(t, "recycled", UniqueName("..", dir), "a name that is not a file name should be replaced")
}

func TestMoveRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	for _, path := range []string{src, dst} {
		require.NoError(t, os.WriteFile(path, []byte(path), 0o600))
	}

	require.ErrorIs(t, Move(src, dst), bin.ErrExists)

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
	require.NoError(t, CopyTree(src, dst))

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

	assert.Equal(t, int64(8), TreeSize(dir), "a directory should report the total size of its contents")
	assert.Equal(t, int64(5), TreeSize(filepath.Join(dir, "a")))
	assert.Equal(t, bin.SizeUnknown, TreeSize(filepath.Join(dir, "missing")))
}

func TestCopyTreeReportsAMissingSource(t *testing.T) {
	dir := t.TempDir()
	assert.Error(t, CopyTree(filepath.Join(dir, "missing"), filepath.Join(dir, "copy")))
}

// A destination that already exists is not overwritten, however the copy is
// reached: the file at dst is somebody's.
func TestCopyFileRefusesAnExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("new"), 0o600))
	require.NoError(t, os.WriteFile(dst, []byte("old"), 0o600))

	require.Error(t, copyFile(src, dst, 0o600))

	content, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "old", string(content), "the destination was overwritten")
}

// A tree it cannot walk has no total, and reporting a partial size as the whole.
func TestTreeSizeReportsUnknownForAnUnreadableTree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode says")
	}
	dir := t.TempDir()
	closed := filepath.Join(dir, "closed")
	require.NoError(t, os.Mkdir(closed, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(closed, "file"), []byte("12345"), 0o600))
	require.NoError(t, os.Chmod(closed, 0o000))
	t.Cleanup(func() { os.Chmod(closed, 0o755) })

	assert.Equal(t, bin.SizeUnknown, TreeSize(dir))
}

func TestIsCrossDevice(t *testing.T) {
	assert.True(t, IsCrossDevice(&os.LinkError{Op: "rename", Err: errCrossDevice}))
	assert.False(t, IsCrossDevice(&os.LinkError{Op: "rename", Err: os.ErrPermission}))
	assert.False(t, IsCrossDevice(os.ErrNotExist))
	assert.False(t, IsCrossDevice(nil))
}

func TestPrepareDest(t *testing.T) {
	dir := t.TempDir()

	dest := filepath.Join(dir, "a", "b", "file.txt")
	require.NoError(t, PrepareDest(dest))
	assert.DirExists(t, filepath.Dir(dest), "the parent directories should have been created")

	require.NoError(t, os.WriteFile(dest, nil, 0o600))
	assert.ErrorIs(t, PrepareDest(dest), bin.ErrExists)
}
