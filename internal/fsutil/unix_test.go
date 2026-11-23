//go:build unix

package fsutil

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// A copy has to refuse what it cannot reproduce.
func TestCopyTreeRefusesWhatItCannotReproduce(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	require.NoError(t, unix.Mkfifo(fifo, 0o600))

	err := CopyTree(fifo, filepath.Join(dir, "copy"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pipe")
	assert.NoFileExists(t, filepath.Join(dir, "copy"))
}

func TestDeviceOf(t *testing.T) {
	dir := t.TempDir()
	dev, err := DeviceOf(dir)
	require.NoError(t, err)

	// Everything inside a directory shares its filesystem.
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))
	subDev, err := DeviceOf(sub)
	require.NoError(t, err)
	assert.Equal(t, dev, subDev)

	_, err = DeviceOf(filepath.Join(dir, "missing"))
	assert.Error(t, err)
}

func TestSameFilesystem(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))

	same, err := SameFilesystem(dir, sub)
	require.NoError(t, err)
	assert.True(t, same)

	_, err = SameFilesystem(dir, filepath.Join(dir, "missing"))
	assert.Error(t, err)
	_, err = SameFilesystem(filepath.Join(dir, "missing"), dir)
	assert.Error(t, err)
}

// A sparse file states a length it does not occupy. DiskUsage counts what it
// occupies, which is what a copy of it needs.
func TestDiskUsageCountsBlocksNotStatedLength(t *testing.T) {
	dir := t.TempDir()
	sparse := filepath.Join(dir, "sparse")
	f, err := os.Create(sparse)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(1<<30))
	require.NoError(t, f.Close())

	dense := filepath.Join(dir, "dense")
	// Random bytes, so a compressing filesystem still has to store them.
	data := make([]byte, 64<<10)
	_, err = rand.Read(data)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dense, data, 0o600))

	assert.Equal(t, int64(1<<30)+64<<10, TreeSize(dir))
	usage := DiskUsage(dir)
	assert.Less(t, usage, int64(1<<20), "the sparse file was counted at its stated length")
	assert.Positive(t, DiskUsage(dense))
	assert.Equal(t, bin.SizeUnknown, DiskUsage(filepath.Join(dir, "missing")))
}

func TestTopDirOf(t *testing.T) {
	dir := t.TempDir()
	dev, err := DeviceOf(dir)
	require.NoError(t, err)

	// Walking up from a directory on a filesystem reaches that filesystem's mount point, which is always.
	top := TopDirOf(dir, dev)
	assert.True(t, dir == top || len(top) < len(dir), "top directory %q is not an ancestor of %q", top, dir)
	topDev, err := DeviceOf(top)
	require.NoError(t, err)
	assert.Equal(t, dev, topDev)

	// A device number that matches nothing.
	assert.Equal(t, dir, TopDirOf(dir, ^uint64(0)))
}

func TestIsStickyDir(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain")
	require.NoError(t, os.Mkdir(plain, 0o755))
	assert.False(t, IsStickyDir(plain))

	sticky := filepath.Join(dir, "sticky")
	require.NoError(t, os.Mkdir(sticky, 0o755))
	require.NoError(t, os.Chmod(sticky, 0o777|os.ModeSticky))
	assert.True(t, IsStickyDir(sticky))

	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	assert.False(t, IsStickyDir(file), "a file is not a sticky directory")

	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(sticky, link))
	assert.False(t, IsStickyDir(link), "a symlink to a sticky directory does not count")

	assert.False(t, IsStickyDir(filepath.Join(dir, "missing")))
}
